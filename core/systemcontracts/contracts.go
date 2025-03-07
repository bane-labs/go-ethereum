package systemcontracts

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// System contracts ABI.
const (
	// governanceABI is a partial ABI of system governing contract, it contains a
	// minimum requires list of methods needed for system interactions and testing.
	governanceABI = `[{"inputs":[],"name":"onPersist","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"onPersistV2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getCurrentConsensus","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidateTo","type":"address"}],"name":"vote","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"getCandidates","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"currentEpochStartHeight","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"epochDuration","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getPendingConsensus","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"sharePeriodDuration","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

	// keyManagementABI is a partial ABI of system anti-MEV KeyManagement contract,
	// it contains a minimum required list of methods needed for system interactions.
	keyManagementABI = `[{"inputs":[],"name":"onPersistV2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"round","type":"uint256"},{"internalType":"uint256","name":"index","type":"uint256"}],"name":"getReshareMsgs","outputs":[{"internalType":"bytes[]","name":"","type":"bytes[]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"round","type":"uint256"},{"internalType":"uint256","name":"index","type":"uint256"}],"name":"getShareMsgs","outputs":[{"internalType":"bytes[]","name":"","type":"bytes[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"indexCurrentNeedRecovering","outputs":[{"internalType":"uint256[]","name":"","type":"uint256[]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"addr","type":"address"}],"name":"indexOfResharing","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"addr","type":"address"}],"name":"indexOfSharing","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"isShareReady","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"addr","type":"address"}],"name":"messagePubkeys","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256[]","name":"idxs","type":"uint256[]"},{"internalType":"bytes[]","name":"messages","type":"bytes[]"}],"name":"recover","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"round","type":"uint256"},{"internalType":"uint256","name":"indexSend","type":"uint256"},{"internalType":"uint256","name":"indexReceive","type":"uint256"}],"name":"recoverMsgs","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes","name":"pvss","type":"bytes"},{"internalType":"bytes[]","name":"messages","type":"bytes[]"}],"name":"reshare","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"bytes","name":"pvss","type":"bytes"},{"internalType":"bytes[]","name":"messages","type":"bytes[]"}],"name":"reshareRecovered","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"round","type":"uint256"},{"internalType":"uint256","name":"index","type":"uint256"}],"name":"rpvsses","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes","name":"pvss","type":"bytes"},{"internalType":"bytes[]","name":"messages","type":"bytes[]"}],"name":"share","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"round","type":"uint256"},{"internalType":"uint256","name":"index","type":"uint256"}],"name":"spvsses","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidate","type":"address"},{"internalType":"string","name":"pubkey","type":"string"}],"name":"registerMessageKey","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"aggregatedCommitments","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"roundNumber","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"epochHeight","type":"uint256"},{"internalType":"uint256","name":"lastEpochHeight","type":"uint256"}],"name":"isRoundNumberIncreased","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"}]`
)

// some storage slot indexes of policy contract.
const blackListSlotIndex = 1
const minGasTipCapSlotIndex = 2
const baseFeeSlotIndex = 3
const envelopeFeeSlotIndex = 5
const maxEnvelopesPerBlockSlotIndex = 6
const maxEnvelopeGasLimitSlotIndex = 7

// A set of genesis contract hashes.
var (
	// GovernanceProxyAdminHash is a hash of the GovernanceProxyAdmin contract that
	// that manages GovernanceProxy contract upgrades.
	GovernanceProxyAdminHash = common.HexToAddress("0x1212000000000000000000000000000000000000")
	// GovernanceProxyHash is a hash of GovernanceProxy contract that manages validators
	// voting and rewards.
	GovernanceProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000001")
	// PolicyProxyHash is a hash of PolicyProxy contract that manages system policy settings.
	PolicyProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000002")
	// GovernanceRewardProxyHash is a hash of GovernanceRewardProxy contract that manages
	// CN and voters reward distribution logic.
	GovernanceRewardProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000003")
	// BridgeProxyHash is a hash of the BridgeProxy contract that manages Bridge funds.
	BridgeProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000004")
	// BridgeManagementProxyHash is a hash of the BridgeManagementProxy contract that
	// manages Bridge operations.
	BridgeManagementProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000005")
	// TreasuryHash is a hash of the Treasury contract that contains all Bridge
	// funds at the start of the network. Note that this contract is not upgradeable.
	TreasuryHash = common.HexToAddress("0x1212000000000000000000000000000000000006")
	// CommitteeMultiSigProxyHash is a hash of the CommitteeMultiSigProxy contract that
	// manages external invocations which needs a Committee agreement.
	CommitteeMultiSigProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000007")
	// KeyManagementProxyHash is a hash of KeyManagementProxy contract that manages
	// distributed keys generation and lifecycle.
	KeyManagementProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000008")
	// Reserved1ProxyHash is a hash of the reserved system contract #1.
	Reserved1ProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000009")
	// Reserved2ProxyHash is a hash of the reserved system contract #2.
	Reserved2ProxyHash = common.HexToAddress("0x121200000000000000000000000000000000000a")
)

// A set of genesis contract ABIs.
var (
	// GovernanceABI is a compiled ABI of Governance contract.
	GovernanceABI abi.ABI
	// KeyManagementABI is a compiled partial ABI of KeyManagement contract.
	KeyManagementABI abi.ABI
)

func init() {
	var err error
	GovernanceABI, err = abi.JSON(strings.NewReader(governanceABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode Governance contract ABI: %w", err))
	}
	KeyManagementABI, err = abi.JSON(strings.NewReader(keyManagementABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode KeyManagement contract ABI: %w", err))
	}
}

// GetMinGasTipCapStateHash computes and returns the storage key of minGasTipCap
// in policy contract, for reading corresponding values from statedb.
func GetMinGasTipCapStateHash() common.Hash {
	return common.BytesToHash([]byte{minGasTipCapSlotIndex})
}

// GetBaseFeeStateHash computes and returns the storage key of baseFee
// in policy contract, for reading corresponding values from statedb.
func GetBaseFeeStateHash() common.Hash {
	return common.BytesToHash([]byte{baseFeeSlotIndex})
}

// GetBlackListStateHash computes and returns the storage key of blockList
// in policy contract with an address, for reading corresponding values from statedb.
func GetBlackListStateHash(addr common.Address) common.Hash {
	return crypto.Keccak256Hash(common.LeftPadBytes(addr.Bytes(), 32), common.LeftPadBytes([]byte{blackListSlotIndex}, 32))
}

// GetEnvelopeFeeStateHash computes and returns the storage key of envelopeFee
// in policy contract, for reading corresponding values from statedb.
func GetEnvelopeFeeStateHash() common.Hash {
	return common.BytesToHash([]byte{envelopeFeeSlotIndex})
}

// GetMaxEnvelopesPerBlockStateHash computes and returns the storage key
// of maxEnvelopesPerBlock in policy contract, for reading corresponding
// values from statedb.
func GetMaxEnvelopesPerBlockStateHash() common.Hash {
	return common.BytesToHash([]byte{maxEnvelopesPerBlockSlotIndex})
}

// GetMaxEnvelopeGasLimitStateHash computes and returns the storage key
// of maxEnvelopeGasLimit in policy contract, for reading corresponding
// values from statedb.
func GetMaxEnvelopeGasLimitStateHash() common.Hash {
	return common.BytesToHash([]byte{maxEnvelopeGasLimitSlotIndex})
}
