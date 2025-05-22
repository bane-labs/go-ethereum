package verifier

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	// validatorsCacheCap is a capacity of validators cache. It's enough to store
	// validators for only three potentially subsequent heights, i.e. three latest
	// blocks to effectivaly verify dBFT payloads travelling through the network and
	// properly initialize dBFT at the latest height.
	validatorsCacheCap = 3
)

// ExtensibleVerifier is a verifier for extensible payloads. It is used to check if
// the sender is allowed to send extensible payloads.
type ExtensibleVerifier struct {
	syncing func() bool

	// various native contract APIs that dBFT uses.
	backend         ethapi.Backend
	validatorsCache *lru.Cache[uint64, []common.Address]
	// dkgIndexCache is a cache for storing the index array of the ordered validators
	dkgIndexCache *lru.Cache[uint64, []int]
}

// NewExtensibleVerifier creates a new extensible verifier. It subscribes to downloader events and
// starts a goroutine to track the downloader state. It also initializes the
// extensible verifier with the provided backend.
func NewExtensibleVerifier(backend ethapi.Backend, syncing func() bool) *ExtensibleVerifier {
	verifier := &ExtensibleVerifier{
		syncing:         syncing,
		backend:         backend,
		validatorsCache: lru.NewCache[uint64, []common.Address](validatorsCacheCap),
		dkgIndexCache:   lru.NewCache[uint64, []int](validatorsCacheCap),
	}

	return verifier
}

// IsExtensibleAllowed determines if address is allowed to send extensible payloads
// (only consensus payloads for now) at the specified height.
func (v *ExtensibleVerifier) IsExtensibleAllowed(h uint64, u common.Address) error {
	// Can't verify extensible sender if the node has an outdated state.
	if v.syncing() {
		return dbftproto.ErrSyncing
	}
	// Only validators are included into extensible whitelist for now.
	validators, err := v.GetValidatorsSorted(&h, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get validators: %w", err)
	}
	_, found := slices.BinarySearchFunc(validators, u, common.Address.Cmp)
	if !found {
		return fmt.Errorf("address is not a validator")
	}
	return nil
}

// GetValidatorsSorted returns validators chosen in the result of the latest
// finalized voting epoch. It calls Governance contract under the hood. The call
// is based on the provided state or (if not provided) on the state of the block
// with the specified height. Validators returned from this method are always
// sorted by bytes order (even if the list returned from governance contract is
// sorted in another way). This method uses cached values in case of validators
// requested by block height.
func (v *ExtensibleVerifier) GetValidatorsSorted(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	res, err := v.getValidators(blockNum, state, header)
	if err != nil {
		return nil, err
	}

	sortedList := slices.Clone(res)
	slices.SortFunc(sortedList, common.Address.Cmp)
	return sortedList, err
}

// getValidators returns validators chosen in the result of the latest finalized
// voting epoch. It calls Governance contract under the hood. The call is based
// on the provided state or (if not provided) on the state of the block with the
// specified height. Validators returned from this method are sorted in the original
// order used by Governance contract. This method uses cached values in case of
// validators requested by block height.
func (v *ExtensibleVerifier) getValidators(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	if state == nil && blockNum != nil {
		vals, ok := v.validatorsCache.Get(*blockNum)
		if ok {
			return vals, nil
		}
	}

	// Perform smart contract call.
	method := "getCurrentConsensus" // latest finalized epoch validators.
	data, err := systemcontracts.GovernanceABI.Pack(method)
	if err != nil {
		return nil, fmt.Errorf("failed to pack '%s': %w", method, err)
	}
	msgData := hexutil.Bytes(data)
	gas := hexutil.Uint64(50_000_000) // more than enough for validators call processing.
	args := ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &systemcontracts.GovernanceProxyHash,
		Data: &msgData,
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel when we are finished consuming integers.
	defer cancel()
	var result *core.ExecutionResult
	if state != nil {
		result, err = ethapi.DoCallAtState(ctx, v.backend, args, state, header, nil, nil, 0, math.MaxUint64)
	} else if blockNum != nil {
		blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(*blockNum))
		result, err = ethapi.DoCall(ctx, v.backend, args, blockNr, nil, nil, 0, math.MaxUint64)
	} else {
		return nil, fmt.Errorf("failed to compute validators: both block number and state are nil")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to perform '%s' call: %w", method, err)
	}
	var res []common.Address
	err = unpackContractExecutionResult(&res, result, systemcontracts.GovernanceABI, method)
	if err != nil {
		return nil, err
	}

	// Update cache in case if existing state was used for validators retrieval.
	if state == nil && blockNum != nil {
		_ = v.validatorsCache.Add(*blockNum, res)
	}

	return res, err
}

func unpackContractExecutionResult(res interface{}, result *core.ExecutionResult, contractAbi abi.ABI, method string) error {
	if len(result.Revert()) > 0 {
		reason, errUnpack := abi.UnpackRevert(result.Revert())
		if errUnpack == nil {
			return fmt.Errorf("%w: %v", vm.ErrExecutionReverted, reason)
		} else {
			return fmt.Errorf("%w, failed to unpack revert reason: %w", vm.ErrExecutionReverted, errUnpack)
		}
	}
	return contractAbi.UnpackIntoInterface(&res, method, result.Return())
}

// GetDKGIndex returns validator dkg index (original validator index +1) by validatorIndex (ordered validator index).
func (v *ExtensibleVerifier) GetDKGIndex(blockNum uint64, validatorIndex int) (int, error) {
	indices, ok := v.dkgIndexCache.Get(blockNum)
	if !ok {
		originValidators, err := v.getValidators(&blockNum, nil, nil)
		if err != nil {
			return -1, err
		}

		indices = make([]int, len(originValidators))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool {
			return originValidators[indices[i]].Cmp(originValidators[indices[j]]) < 0
		})
		_ = v.dkgIndexCache.Add(blockNum, indices)
	}

	if validatorIndex < 0 || validatorIndex >= len(indices) {
		return -1, fmt.Errorf("invalid validator index: validators count is %d, requested %d", len(indices), validatorIndex)
	}
	dkgIndex := indices[validatorIndex] + 1
	return dkgIndex, nil
}
