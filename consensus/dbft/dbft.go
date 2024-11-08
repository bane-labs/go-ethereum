// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package dbft implements the proof-of-authority consensus engine.
package dbft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/dbft/dbftutil"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/eth/downloader"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"github.com/nspcc-dev/dbft"
	"github.com/nspcc-dev/dbft/timer"
	"go.uber.org/zap"
	"golang.org/x/crypto/sha3"
	"golang.org/x/exp/slices"
)

// DBFT proof-of-authority protocol constants.
var (
	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.

	diffInTurn = big.NewInt(2) // Block difficulty for in-turn signatures
	diffNoTurn = big.NewInt(1) // Block difficulty for out-of-turn signatures

	emptyWithdrawals = make([]*types.Withdrawal, 0)
)

const (
	extraSeal = crypto.SignatureLength // Fixed number of extra-data suffix bytes reserved for a single signer seal
	// txSubCap is the capacity of channel that receives transaction notifications from mempool.
	txSubCap = 100
	// msgsChCap is a capacity of channel that accepts consensus messages from
	// dBFT protocol.
	msgsChCap = 100
	// validatorsCacheCap is a capacity of validators cache. It's enough to store
	// validators for only three potentially subsequent heights, i.e. three latest
	// blocks to effectivaly verify dBFT payloads travelling through the network and
	// properly initialize dBFT at the latest height.
	validatorsCacheCap = 3
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")

	// errMissingVanity is returned if a block's extra-data section is shorter than
	// 32 bytes, which is required to store the signer vanity.
	errMissingVanity = errors.New("extra-data 32 byte vanity prefix missing")

	// errMissingSignature is returned if a block's extra-data section doesn't seem
	// to contain a 65 byte secp256k1 signature.
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	// errInvalidExtraSigners is returned if a block extra contains an invalid list of
	// signers (i.e. non divisible by 20 bytes)
	errInvalidExtraSigners = errors.New("invalid validators list in extra-data")

	// errInvalidMixDigest is returned if a block's mix digest is non-zero.
	errInvalidMixDigest = errors.New("zero mix digest (NextConsensus)")

	// errInvalidUncleHash is returned if a block contains an non-empty uncle list.
	errInvalidUncleHash = errors.New("non empty uncle hash")

	// errInvalidDifficulty is returned if the difficulty of a block neither 1 or 2.
	errInvalidDifficulty = errors.New("invalid difficulty")

	// errWrongDifficulty is returned if the difficulty of a block doesn't match the
	// turn of the signer.
	errWrongDifficulty = errors.New("wrong difficulty")

	// errInvalidTimestamp is returned if the timestamp of a block is lower than
	// the previous block's timestamp + the minimum block period.
	errInvalidTimestamp = errors.New("invalid timestamp")

	// errUnauthorizedSigner is returned if a header is signed by a non-authorized entity.
	errUnauthorizedSigner = errors.New("unauthorized signer")
)

// getSignersAndSigs extracts the set of validators addresses (len(cfg.StandByValidators) number of them)
// and a set of validators signatures (BFT number of them) from a signed header.
func getSignersAndSigs(cfg *config, extra []byte) ([]common.Address, [][]byte, error) {
	// Retrieve the signature from the header extra-data
	var (
		n             = len(cfg.StandByValidators)
		m             = crypto.GetBFTHonestNodeCount(n)
		addrsBytesLen = common.AddressLength * n
		sigsBytesLen  = extraSeal * m
		addrs         = make([]common.Address, n)
		sigs          = make([][]byte, m)
	)
	switch extra[0] {
	case dbftutil.ExtraV0:
		if len(extra) < dbftutil.ExtraVersionLen+addrsBytesLen+sigsBytesLen {
			return nil, nil, errMissingSignature
		}
		// Recover Ethereum addresses of validators and their signatures, preserve
		// the order that was specified in the source extra, because validators are
		// sorted and NextConsensus depends on it.
		for i := range addrs {
			addrOffset := dbftutil.ExtraVersionLen + i*common.AddressLength
			copy(addrs[i][:], extra[addrOffset:addrOffset+common.AddressLength])
		}
		for i := range sigs {
			sigOffset := len(extra) - sigsBytesLen + i*extraSeal
			sigs[i] = extra[sigOffset : sigOffset+extraSeal]
		}
	default:
		return nil, nil, fmt.Errorf("unexpected Extra version: %d", extra[0])
	}

	return addrs, sigs, nil
}

// DBFT is the proof-of-authority consensus engine.
type DBFT struct {
	messages chan Payload
	// Broadcast is a callback which is called to notify the dBFT service
	// about a new consensus payload to be sent.
	broadcast func(m *dbftproto.Message) error

	// requestTxs is a callback which is called to request the missing
	// transactions from neighbor nodes. Requested transactions hashes are
	// stored in txCbList and checked against the incoming transactions. If
	// subsequent incoming transaction was requested then it'll be sent to the
	// buffered txs channel.
	requestTxs func(hashed []common.Hash)
	txCbList   atomic.Value
	txs        chan *types.Transaction

	// various chain/mempool events and subscription management:
	chainHeadSub    event.Subscription
	chainHeadEvents chan core.ChainHeadEvent
	// mux is a subscriptions dispatcher for various chain events including chain
	// downloader events. Downloader events are used to track miner's state since
	// miner work may be temporary suspended due to the node sync.
	mux *event.TypeMux
	// syncing indicates whether the node is still syncing. This variable is updated
	// irrespectively from the engine activity, and thus, may be relied on even when
	// dBFT engine is not started.
	syncing atomic.Bool

	config *config // Consensus engine configuration parameters

	signer       common.Address    // Ethereum address of the signing key
	signFn       SignerFn          // Signer function to authorize hashes with
	amevKeystore *antimev.KeyStore // anti-MEV keystore responsible for DKG and encrypted transactions decryption
	lock         sync.RWMutex      // Protects signer, signFn and amevKeystore fields

	dbft             *dbft.DBFT[common.Hash]
	dbftStarted      atomic.Bool
	eventLoopStarted atomic.Bool
	blockQueue       *blockQueue

	// lastTimestamp, lastIndex and lastBlockHash are updated on every new header
	// received from dBFT or from chain. These fields have exactly those type
	// that Eth offers, thus, they need to be converted before feeding to dBFT.
	lastTimestamp     uint64 // in seconds, like Eth requires.
	lastIndex         uint64
	lastBlockHash     common.Hash
	lastBlockSealHash common.Hash
	lastBlockExtra    []byte

	// lastProposal holds the latest proposal submitted to dBFT by miner. It is updated
	// irrespectively and concurrently to dBFT process, thus, access should be protected
	// by mutex.
	lastProposalLock sync.RWMutex
	lastProposal     *types.Block

	// sealingProposal holds current proposal dBFT is working on. It's not protected by
	// mutex since every access point is controlled by eventLoop, thus, not concurrent.
	sealingProposal     *types.Header
	sealingTransactions types.Transactions

	// chain and mempool instances needed for proper dBFT callbacks functioning.
	chain      ChainHeaderReader
	txpool     txPool
	legacypool legacyPool

	quit     chan struct{}
	finished chan struct{}

	// various native contract APIs that dBFT uses.
	ethAPI          *ethapi.BlockChainAPI
	txAPI           *ethapi.TransactionAPI
	validatorsCache *lru.Cache[uint64, []common.Address]

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications

	// The fields for dkg
	targetHeight     uint64
	epochStartHeight uint64
	shareDuration    uint64
	round            uint64
	consensusList    []common.Address
	txWatchList      []*TxWatchRetry
}

// config represents Engine configuration.
type config struct {
	*params.DBFTConfig
	dkgEnablingHeight     int64
	antiMEVEnablingHeight int64
}

// New creates a DBFT proof-of-authority consensus engine with the initial
// signers set to the ones provided by the user.
func New(chainCfg *params.ChainConfig, _ ethdb.Database) (*DBFT, error) {
	cfg := &config{
		DBFTConfig:            chainCfg.DBFT,
		dkgEnablingHeight:     -1,
		antiMEVEnablingHeight: -1,
	}
	if cfg.SecondsPerBlock == 0 {
		return nil, errors.New("zero-period dBFT chain is not supported")
	}
	if cfg.Coinbase == (common.Address{}) {
		return nil, errors.New("empty dBFT Coinbase is not allowed, need to specify mining rewards receiver")
	}
	// Set any missing consensus parameters to their defaults
	bftCfg := *cfg.DBFTConfig
	// Sort validators once to reuse the sorted list in getNextConsensus.
	// Do not change configured committee.
	bftCfg.StandByValidators = slices.Clone(cfg.StandByValidators)
	slices.SortFunc(bftCfg.StandByValidators, common.Address.Cmp)
	cfg.DBFTConfig = &bftCfg

	if chainCfg.NeoXDKGBlock != nil {
		cfg.dkgEnablingHeight = chainCfg.NeoXDKGBlock.Int64()
	}
	if chainCfg.NeoXAMEVBlock != nil {
		cfg.antiMEVEnablingHeight = chainCfg.NeoXAMEVBlock.Int64()
	}

	c := &DBFT{
		config:     cfg,
		blockQueue: newBlockQueue(),

		messages:        make(chan Payload, msgsChCap),
		txs:             make(chan *types.Transaction, txSubCap),
		chainHeadEvents: make(chan core.ChainHeadEvent, 2),

		quit:     make(chan struct{}),
		finished: make(chan struct{}),

		validatorsCache: lru.NewCache[uint64, []common.Address](validatorsCacheCap),
	}

	var err error
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dBFT logger: %w", err)
	}
	c.dbft, err = dbft.New[common.Hash](
		dbft.WithTimer[common.Hash](timer.New()),
		dbft.WithLogger[common.Hash](logger),
		dbft.WithSecondsPerBlock[common.Hash](time.Duration(bftCfg.SecondsPerBlock)*time.Second),
		dbft.WithGetKeyPair[common.Hash](func(keys []dbft.PublicKey) (int, dbft.PrivateKey, dbft.PublicKey) {
			c.lock.RLock()
			signer, signFn, ks := c.signer, c.signFn, c.amevKeystore
			c.lock.RUnlock()

			// Bail out if we're unauthorized to sign a block
			for i, validator := range keys {
				if validator.(*PublicKey).Account.Cmp(signer) == 0 {
					s := &Signer{
						Signer:       signer,
						SignFn:       signFn,
						AmevKeystore: ks,
					}
					return i,
						s,
						validator // This "public key" is not used by dBFT in any way, but we can provide it, so let it be here.
				}
			}

			return -1, nil, nil
		}),
		// Consensus engine doesn't have access to the blockchain at the moment of call to constructor. Thus,
		// we use these `lastIndex` and `lastBlockHash` fields cached in the service.
		dbft.WithCurrentHeight[common.Hash](func() uint32 {
			return uint32(c.lastIndex)
		}),
		dbft.WithCurrentBlockHash[common.Hash](func() common.Hash {
			return c.lastBlockHash
		}),
		dbft.WithGetValidators[common.Hash](func(txs ...dbft.Transaction[common.Hash]) []dbft.PublicKey {
			if c.lastBlockHash.Cmp(common.Hash{}) == 0 {
				// Program bug.
				panic("last block hash wasn't initialized")
			}

			var (
				pKeys []common.Address
				err   error
			)
			if txs == nil {
				// getValidators with empty args is used by dbft to fill the list of
				// block's validators, thus should return validators from the current
				// epoch without recalculation.
				pKeys, err = c.getValidators(&c.lastIndex, nil, nil)
			}
			// getValidators with non-empty args is used by dbft to fill block's
			// NextConsensus field, but DBFT doesn't provide WithGetConsensusAddress
			// callback and fills NextConsensus by itself via WithNewBlockFromContext
			// callback. Thus, leave pKeys empty if txes != nil.
			if err != nil {
				// Program bug.
				panic(fmt.Errorf("failed to retrieve next block validators: %w", err))
			}
			res := make([]dbft.PublicKey, len(pKeys))
			for i, s := range pKeys {
				res[i] = &PublicKey{
					Account: s,
				}
			}
			return res
		}),
		dbft.WithProcessBlock[common.Hash](func(b dbft.Block[common.Hash]) {
			dbftBlock := b.(*Block)
			if uint64(dbftBlock.Index()) <= c.lastIndex {
				return
			}

			// Avoid copying and may safely change the block itself, as this part
			// of code is guaranteed to be called once by dBFT.
			dbftBlock.header.Extra = append(dbftBlock.header.Extra, c.getBlockWitness()...) // Extra version isn't changed, validators addresses and signatures are added.

			res := types.NewBlockWithHeader(dbftBlock.header).WithBody(dbftBlock.transactions, nil).WithWithdrawals(dbftBlock.withdrawals)

			// Firstly, notify chain about new block.
			if err := c.blockQueue.PutBlock(res, dbftBlock.state, dbftBlock.receipts); err != nil {
				// The block might already be added via the regular network
				// interaction.
				if h := c.chain.GetHeaderByNumber(res.Number().Uint64()); h == nil {
					log.Warn("error on enqueue block", "error", err.Error())
				}
			}

			c.postBlock(res.Header())
		}),
		dbft.WithNewBlockFromContext[common.Hash](func(ctx *dbft.Context[common.Hash]) dbft.Block[common.Hash] {
			if !c.chain.Config().IsNeoXAMEV(big.NewInt(int64(ctx.BlockIndex))) {
				prepareReq := ctx.PreparationPayloads[ctx.PrimaryIndex]
				if prepareReq == nil {
					panic("can't create new block from context: prepare request is nil")
				}
				// Reuse PreBlock helper to avoid code duplications.
				pre := c.newPreBlockFromContext(prepareReq.GetPrepareRequest().(*prepareRequest).SealingProposal)
				res := &Block{
					isLegacy:    true,
					header:      pre.header,
					withdrawals: pre.withdrawals,
				}
				return res
			}
			pre := ctx.PreBlock().(*PreBlock)
			// Recalculate Block state based on PreBlock and updated transactions list.
			// Avoid changing the original PreHeader.
			b := &PreBlock{
				header:       pre.header,
				transactions: pre.finalTransactions,
				withdrawals:  pre.withdrawals,
			}
			ethBlock := b.ToEthBlock()
			state, receipts, _, gasUsed, err := c.chain.ProcessState(ethBlock, nil)
			if err != nil {
				log.Crit("failed to process final Block state from PreBlock",
					"err", err,
					"number", ethBlock.NumberU64(),
					"seal hash", c.SealHash(ethBlock.Header()),
					"parent hash", ethBlock.ParentHash().String(),
					"intermediate merkle root", ethBlock.Root(),
					"coinbase", ethBlock.Coinbase().String(),
					"gas limit", ethBlock.GasLimit(),
					"gas used", ethBlock.GasUsed(),
					"difficulty", ethBlock.Difficulty().String(),
					"mix digest", ethBlock.MixDigest().String(),
					"nonce", ethBlock.Nonce(),
					"time", ethBlock.Time(),
					"uncle hash", ethBlock.UncleHash().String(),
					"txs", len(ethBlock.Transactions()))
			}
			// Manually update header's fields based on fresh state.
			h := ethBlock.Header()
			h.GasUsed = gasUsed

			// Use a copy of state to avoid changing block's state. The original state will be reused
			// during block insertion into chain.
			nextVals, err := c.getValidators(nil, state.Copy(), h)
			if err != nil {
				log.Crit("Failed to compute next block validators while constructing final Block",
					"err", err)
			}
			h.MixDigest = dbftutil.GetNextConsensusHash(nextVals)

			// Update state root, transactions root, receipts hash and bloom.
			res, err := c.FinalizeAndAssemble(c.chain, h, state, pre.finalTransactions, nil, receipts, ethBlock.Withdrawals())
			if err != nil {
				log.Crit("Failed to finalize and assemble final Block",
					"err", err)
			}

			return &Block{
				header:              res.Header(),
				withdrawals:         res.Withdrawals(),
				transactions:        res.Transactions(),
				localSignatureBytes: nil,
				state:               state,
				receipts:            receipts,
			}
		}),
		dbft.WithWatchOnly[common.Hash](func() bool {
			return false
		}),
		dbft.WithGetTx[common.Hash](func(h common.Hash) dbft.Transaction[common.Hash] {
			tx := c.txpool.Get(h)
			// This check is needed, because in case of missing transaction dBFT
			// expects a pure nil.
			if tx != nil {
				return &Transaction{
					Tx: tx,
				}
			}

			// Do not try to retrieve on-chain transaction.
			return nil
		}),
		dbft.WithGetVerified[common.Hash](func() []dbft.Transaction[common.Hash] {
			var txs types.Transactions
			// Check the sealing proposal, because c.sealingTransactions may be nil
			// in case of missing pending transactions, and it's OK.
			if c.sealingProposal == nil {
				// Program bug.
				panic("missing pending sealing work")
			}
			txs = c.sealingTransactions

			res := make([]dbft.Transaction[common.Hash], len(txs))
			for i := range txs {
				res[i] = &Transaction{
					Tx: txs[i],
				}
			}
			return res
		}),
		dbft.WithRequestTx[common.Hash](func(hashes ...common.Hash) {
			if len(hashes) == 0 {
				return
			}

			sorted := slices.Clone(hashes)
			slices.SortFunc(sorted, common.Hash.Cmp)
			c.txCbList.Store(sorted)

			c.requestTxs(sorted)
		}),
		dbft.WithStopTxFlow[common.Hash](func() {
			var hashes []common.Hash
			c.txCbList.Store(hashes)
		}),
		dbft.WithNewConsensusPayload[common.Hash](c.newPayload),
		dbft.WithNewPrepareRequest[common.Hash](func(ts uint64, nonce uint64, txHashes []common.Hash) dbft.PrepareRequest[common.Hash] {
			var req = new(prepareRequest)
			if c.sealingProposal == nil {
				panic("bug: sealing proposal is not initialized")
			}

			// Recalculate block state provided by miner and update sealing proposal if context-related
			// block fields were changed.
			dbftBlock := c.newPreBlockFromContext(c.sealingProposal)
			dbftBlock.transactions = c.sealingTransactions
			ethBlock := dbftBlock.ToEthBlock()

			state, _, _, gasUsed, err := c.chain.ProcessState(ethBlock, nil)
			if err != nil {
				log.Crit("failed to process state from proposal",
					"err", err,
					"number", ethBlock.NumberU64(),
					"seal hash", c.SealHash(ethBlock.Header()),
					"parent hash", ethBlock.ParentHash().String(),
					"intermediate merkle root", ethBlock.Root(),
					"coinbase", ethBlock.Coinbase().String(),
					"gas limit", ethBlock.GasLimit(),
					"gas used", ethBlock.GasUsed(),
					"difficulty", ethBlock.Difficulty().String(),
					"mix digest", ethBlock.MixDigest().String(),
					"nonce", ethBlock.Nonce(),
					"time", ethBlock.Time(),
					"uncle hash", ethBlock.UncleHash().String(),
					"txs", len(ethBlock.Transactions()))
			}

			header := ethBlock.Header()
			header.GasUsed = gasUsed
			header.Root = state.IntermediateRoot(c.chain.Config().IsEIP158(header.Number))

			c.sealingProposal = header

			// Fill NextConsensus based on the currently accepting block state and update MixDigest.
			nextVals, err := c.getValidators(nil, state, c.sealingProposal)
			if err != nil {
				log.Crit("Failed to compute next block validators",
					"err", err)
			}
			c.sealingProposal.MixDigest = dbftutil.GetNextConsensusHash(nextVals)

			req.SealingProposal = c.sealingProposal
			req.ParentSealHash = c.lastBlockSealHash
			req.ParentExtra = c.lastBlockExtra
			req.TxHashes = txHashes

			return req
		}),
		dbft.WithNewCommit[common.Hash](func(sig []byte) dbft.Commit {
			res := new(commit)
			copy(res.SignatureExt[:], sig)
			return res
		}),
		dbft.WithNewPrepareResponse[common.Hash](func(prepH common.Hash) dbft.PrepareResponse[common.Hash] {
			return &prepareResponse{
				PreparationHashExt: prepH,
			}
		}),
		dbft.WithNewChangeView[common.Hash](func(newView byte, reason dbft.ChangeViewReason, ts uint64) dbft.ChangeView {
			return &changeView{
				newViewNumber: newView,
				ReasonExt:     reason,
				TimestampExt:  ts,
			}
		}),
		dbft.WithNewRecoveryRequest[common.Hash](func(ts uint64) dbft.RecoveryRequest {
			return &recoveryRequest{
				TimestampExt: ts,
			}
		}),
		dbft.WithNewRecoveryMessage[common.Hash](func() dbft.RecoveryMessage[common.Hash] { return new(recoveryMessage) }),
		dbft.WithVerifyPrepareResponse[common.Hash](func(_ dbft.ConsensusPayload[common.Hash]) error { return nil }),
		dbft.WithVerifyPreCommit[common.Hash](func(preCommit dbft.ConsensusPayload[common.Hash]) error { return nil }),
		dbft.WithVerifyPrepareRequest[common.Hash](func(p dbft.ConsensusPayload[common.Hash]) error {
			req := p.GetPrepareRequest().(*prepareRequest)
			if req.SealingProposal == nil {
				return errors.New("failed to verify PrepareRequest: sealing proposal is nil")
			}
			// Do not verify MixDigest since it depends on block state and will be verified once all transactions
			// are fetched.
			parent := c.chain.GetBlockByNumber(req.SealingProposal.Number.Uint64() - 1)
			if parent == nil {
				return fmt.Errorf("no parent found for height %d", req.SealingProposal.Number.Uint64()-1)
			}
			parentHeader := parent.Header()
			err := c.verifyHeader(c.chain, req.SealingProposal, []*types.Header{parentHeader}, false)
			if err != nil {
				return fmt.Errorf("invalid header: %w", err)
			}
			if req.SealingProposal.ParentHash != c.lastBlockHash {
				// Genesis block  is hard-coded, thus its hash (as a parent hash) must always match
				// the one that prepareRequest declares as a parent hash, otherwise it's an error.
				if c.dbft.BlockIndex <= 1 {
					return fmt.Errorf("invalid parent: expected %s, got %s", c.lastBlockHash, req.SealingProposal.ParentHash)
				}
				if req.ParentSealHash != c.lastBlockSealHash {
					return fmt.Errorf("parent seal hash doesn't match the last block seal hash: expected %s, got %s", c.lastBlockSealHash, req.ParentSealHash)
				}
				// Verify proposed parent's signature.
				savedGrandparent := c.chain.GetBlockByNumber(req.SealingProposal.Number.Uint64() - 2)
				if savedGrandparent == nil {
					return errors.New("failed to verify parent: failed to retrieve grandparent from storage")
				}
				_, err := c.verifyExtra(req.ParentSealHash, req.ParentExtra, savedGrandparent.Header().NextConsensus())
				if err != nil {
					return fmt.Errorf("invalid parent: parent's witness verification failed: %w", err)
				}

				// After that we assume that parent block is totally valid, and it can be inserted to chain.
				// Internal fork resolving mechanism will deal with forks.
				oldHash := c.lastBlockHash
				oldExtra := c.lastBlockExtra
				parentHeader.Extra = req.ParentExtra
				err = c.blockQueue.PutBlock(parent.WithSeal(parentHeader), nil, nil)
				if err != nil {
					err = fmt.Errorf("failed to enqueue parent with updated extra for height %d (old hash %s, new hash %s): %w",
						req.SealingProposal.Number.Uint64()-1,
						parent.Hash(),
						req.SealingProposal.ParentHash,
						err)
					// This error is critical for further dBFT functioning.
					log.Warn(err.Error())
					return err
				}

				c.lastBlockHash = req.SealingProposal.ParentHash
				c.lastBlockExtra = req.ParentExtra
				c.dbft.PrevHash = req.SealingProposal.ParentHash

				log.Info("New parent stored",
					"number", parentHeader.Number,
					"old hash", oldHash.String(),
					"new hash", req.SealingProposal.ParentHash.String(),
					"sealhash", req.ParentSealHash.String(),
					"old extra", hex.EncodeToString(oldExtra),
					"new extra", hex.EncodeToString(req.ParentExtra))
			}

			c.sealingProposal = req.SealingProposal

			// Do not fill c.sealingTransactions. If the node is primary, then sealing txs must be
			// properly filled by this moment from the new miner proposal in Seal (it happens even
			// before the dBFT initialisation for this round). If the node is backup, then
			// sealingTransactions are not needed for proper dBFT functioning (dBFT will collect
			// transactions via internal mechanism in this consensus view).
			c.sealingTransactions = nil

			return nil
		}),
		dbft.WithVerifyPreBlock[common.Hash](func(b dbft.PreBlock[common.Hash]) bool {
			dbftBlock := b.(*PreBlock)
			parent := c.chain.CurrentBlock()
			if parent.Number.Cmp(dbftBlock.header.Number) >= 0 {
				log.Warn("proposed PreBlock has already outdated",
					"current block number", parent.Number.Uint64(),
					"proposed block number", dbftBlock.header.Number)
				return false
			}
			if c.lastTimestamp > dbftBlock.header.Time {
				log.Warn("proposed PreBlock has small timestamp",
					"ts", dbftBlock.header.Time,
					"last", c.lastTimestamp)
				return false
			}
			ethBlock := dbftBlock.ToEthBlock()
			_, _, err := c.chain.VerifyBlock(ethBlock, false)
			if err != nil {
				log.Warn("proposed PreBlock verification failed",
					"err", err.Error())
				return false
			}

			return true
		}),
		dbft.WithVerifyBlock[common.Hash](func(b dbft.Block[common.Hash]) bool {
			if !c.chain.Config().IsNeoXAMEV(big.NewInt(int64(c.dbft.Context.BlockIndex))) {
				dbftBlock := b.(*Block)
				parent := c.chain.CurrentBlock()
				if parent.Number.Cmp(dbftBlock.header.Number) >= 0 {
					log.Warn("proposed block has already outdated",
						"current block number", parent.Number.Uint64(),
						"proposed block number", dbftBlock.header.Number)
					return false
				}
				if c.lastTimestamp > dbftBlock.header.Time {
					log.Warn("proposed block has small timestamp",
						"ts", dbftBlock.header.Time,
						"last", c.lastTimestamp)
					return false
				}

				ethBlock := types.NewBlockWithHeader(dbftBlock.header).WithBody(dbftBlock.transactions, nil).WithWithdrawals(dbftBlock.withdrawals)
				state, receipts, err := c.chain.VerifyBlock(ethBlock, true)
				if err != nil {
					log.Warn("proposed block verification failed",
						"err", err.Error())
					return false
				}

				// Verify NextConsensus based on the state got after in-block transactions processing. Make a
				// state copy in order to avoid state modifications potentially made by getValidators call.
				// The original state will be committed if block is accepted.
				nextVals, err := c.getValidators(nil, state.Copy(), dbftBlock.header)
				if err != nil {
					log.Crit("Failed to compute next block validators",
						"err", err)
				}
				expectedMixDigest := dbftutil.GetNextConsensusHash(nextVals)
				if dbftBlock.header.MixDigest != expectedMixDigest {
					log.Warn("Invalid NextConsensus in the proposed block",
						"expected", expectedMixDigest.String(),
						"actual", dbftBlock.header.MixDigest.String())
					return false
				}

				dbftBlock.state = state
				dbftBlock.receipts = receipts

				return true
			}

			log.Crit("unexpected call to VerifyBlock")

			return false
		}),
		dbft.WithBroadcast[common.Hash](func(p dbft.ConsensusPayload[common.Hash]) {
			if err := p.(*Payload).Sign(c.dbft.Priv.(*Signer)); err != nil {
				log.Warn("can't sign consensus payload", "error", err)
			}

			ep := &p.(*Payload).Message
			err := c.broadcast(ep)
			if err != nil {
				log.Warn("can't broadcast consensus message", "error", err)
			}
		}),
		dbft.WithAntiMEVExtensionEnablingHeight[common.Hash](c.config.antiMEVEnablingHeight),
		dbft.WithNewPreCommit[common.Hash](func(data []byte) dbft.PreCommit {
			return &preCommit{
				dataExt: data,
			}
		}),
		dbft.WithNewPreBlockFromContext[common.Hash](func(ctx *dbft.Context[common.Hash]) dbft.PreBlock[common.Hash] {
			prepareReq := ctx.PreparationPayloads[ctx.PrimaryIndex]
			if prepareReq == nil {
				panic("can't create new PreBlock from context: prepare request is nil")
			}

			return c.newPreBlockFromContext(prepareReq.GetPrepareRequest().(*prepareRequest).SealingProposal)
		}),
		dbft.WithProcessPreBlock(func(b dbft.PreBlock[common.Hash]) error {
			var (
				ctx = c.dbft.Context
				pre = ctx.PreBlock().(*PreBlock)
			)
			// A short path: there's no envelopes at all, use proposed transactions as-is.
			if len(pre.envelopesData) == 0 {
				pre.finalTransactions = pre.transactions
				return nil
			}
			shares := make(map[int][]*tpke.DecryptionShare)
			for _, preC := range ctx.PreCommitPayloads {
				if preC != nil && preC.ViewNumber() == ctx.ViewNumber {
					dkgIndex, err := c.getDKGIndex(int(preC.ValidatorIndex()), pre.header.Number.Uint64())
					if err != nil {
						return fmt.Errorf("get DKG index failed: ValidatorIndex %d, block height %d", int(preC.ValidatorIndex()), pre.header.Number.Uint64())
					}
					// Indexes in shares map must use dkg index.
					shares[dkgIndex] = preC.GetPreCommit().(*preCommit).Shares()
				}
			}
			var (
				encryptedKeys = make([]*tpke.CipherText, len(pre.envelopesData))
				encryptedMsgs = make([][]byte, len(pre.envelopesData))
			)
			for i := range pre.envelopesData {
				encryptedKeys[i] = pre.envelopesData[i].encryptedKey
				encryptedMsgs[i] = pre.envelopesData[i].encryptedMsg
			}
			c.lock.RLock()
			ks := c.amevKeystore
			c.lock.RUnlock()

			decryptedTxsBytes, err := ks.AggregateAndDecryptWithShare(encryptedKeys, encryptedMsgs, shares)
			if err != nil {
				// Some shares are invalid, valid shares isn't enough to decrypt, wait for more shares to be collected.
				return fmt.Errorf("failed to decrypt Encrypted transactions, not enough valid shares: %w", err)
			}

			if len(decryptedTxsBytes) != len(pre.envelopesData) {
				// Some shares are invalid, valid shares isn't enough to decrypt, wait for more shares to be collected.
				return fmt.Errorf("invalid number of Decrypted transactions: expected %d, actual %d", len(pre.envelopesData), len(decryptedTxsBytes))
			}
			var (
				txx = make([]*types.Transaction, len(pre.transactions))
				j   int
			)
			for i := range pre.transactions {
				if pre.envelopesData[j].index != i || // pre.transactions[i] is not an envelope, use it as-is.
					decryptedTxsBytes[j] == nil { // pre.transactions[i] is Envelope, but its content failed to be decrypted, use Envelope as-is.
					txx[i] = pre.transactions[i]
					continue
				}
				log.Info("Envelope data decrypted",
					"envelope hash", pre.transactions[i].Hash(),
					"envelope index", i,
					"data", hex.EncodeToString(decryptedTxsBytes[j]))
				var decryptedTx = new(types.Transaction)
				err := decryptedTx.DecodeRLP(rlp.NewStream(bytes.NewReader(decryptedTxsBytes[j]), 0))
				if err != nil {
					log.Info("Decrypted transaction decoding failed",
						"envelope hash", pre.transactions[i].Hash(),
						"envelope index", i,
						"data", hex.EncodeToString(decryptedTxsBytes[j]),
						"error", err.Error())
					txx[i] = pre.transactions[i]
					j++
					continue
				}
				err = c.legacypool.ValidateDecryptedTx(decryptedTx, pre.transactions[i])
				if err != nil {
					txx[i] = pre.transactions[i]
					log.Info("Decrypted transaction is invalid",
						"envelope hash", pre.transactions[i].Hash(),
						"envelope index", i,
						"data", hex.EncodeToString(decryptedTxsBytes[j]),
						"error", err.Error())
					j++
					continue
				}
				txx[i] = decryptedTx
				j++
				continue
			}
			pre.finalTransactions = txx
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dBFT: %w", err)
	}

	return c, nil
}

// newPreBlockFromContext is a dBFT callback that builds [PreBlock] from the provided dBFT proposal
// filling all appropriate fields from dBFT context. It doesn't set transactions or any
// other information except the header itself.
func (c *DBFT) newPreBlockFromContext(sealingProposal *types.Header) *PreBlock {
	ctx := c.dbft.Context

	// Avoid changing PrepareRequest itself.
	h := types.CopyHeader(sealingProposal)

	// BlockIndex -> Number
	h.Number = big.NewInt(int64(ctx.BlockIndex))

	// PrimaryIndex -> Nonce
	binary.BigEndian.PutUint64(h.Nonce[:], uint64(ctx.PrimaryIndex))

	h.Difficulty = c.getDifficulty(int(ctx.PrimaryIndex), uint64(ctx.BlockIndex))

	// Do not fill PreBlock's transactions. First of all, transactions are not
	// needed for block signing or block signature verification. Secondly, some
	// transactions may be missing by the moment of call to NewBlockFromContext
	// (dBFT has only the full set of their hashes). Once all transactions are
	// fetched and the commits are collected, SetTransactions callback will be
	// called by dBFT library to properly initialize PreBlock's transactions.
	res := &PreBlock{header: h}
	// Withdrawals are temporary empty if Shanghai is passed.
	if c.chain.Config().IsShanghai(h.Number, h.Time) {
		res.withdrawals = emptyWithdrawals
	}

	return res
}

func (c *DBFT) getBlockWitness() []byte {
	dctx := c.dbft.Context

	// Validators sorting order is guaranteed by governance contract, they are sorted
	// by bytes order, thus, no additional sorting here.
	vals := make([]common.Address, len(dctx.Validators))
	for i := range dctx.Validators {
		vals[i] = dctx.Validators[i].(*PublicKey).Account
	}
	res := dbftutil.FlattenAddresses(vals)

	sigs := make(map[common.Address][]byte)
	for i := range vals {
		if p := dctx.CommitPayloads[i]; p != nil && p.ViewNumber() == dctx.ViewNumber {
			sigs[vals[i]] = p.GetCommit().Signature()
		}
	}
	m := c.dbft.Context.M()

	// Signatures sorting order is the same as corresponding *sorted* validators order.
	for i, j := 0, 0; i < len(vals) && j < m; i++ {
		if sig, ok := sigs[vals[i]]; ok {
			res = append(res, sig...)
			j++
		}
	}

	return res
}

// WithEthAPI initializes Eth blockchain API for proper consensus module work.
func (c *DBFT) WithEthAPI(api *ethapi.BlockChainAPI) {
	c.ethAPI = api
}

// WithTxAPI initializes Eth transaction API for sending transaction.
func (c *DBFT) WithTxAPI(api *ethapi.TransactionAPI) {
	c.txAPI = api
}

// WithBroadcast sets callback to notify the caller about new consensus message.
func (c *DBFT) WithBroadcast(f func(m *dbftproto.Message) error) {
	c.broadcast = f
}

// WithRequestTxs sets callback to request the missing transactions from neighbor nodese.
func (c *DBFT) WithRequestTxs(f func(hashed []common.Hash)) {
	c.requestTxs = f
}

// WithMux sets subscriptions dispatcher service to provide a way for dBFT to watch
// over chain downloader progress. It's needed because miner work is suspended during
// the ongoing node sync process.
func (c *DBFT) WithMux(mux *event.TypeMux) {
	c.mux = mux
	c.blockQueue.SetMux(mux)

	go c.syncWatcher()
}

// syncWatcher is a standalone loop aimed to be active irrespectively of dBFT engine
// activity. It tracks the first chain sync attempt till its end.
func (c *DBFT) syncWatcher() {
	downloaderEvents := c.mux.Subscribe(downloader.StartEvent{}, downloader.DoneEvent{}, downloader.FailedEvent{})
	defer func() {
		if !downloaderEvents.Closed() {
			downloaderEvents.Unsubscribe()
		}
	}()
	dlEventCh := downloaderEvents.Chan()

events:
	for {
		select {
		case <-c.quit:
			break events
		case ev := <-dlEventCh:
			if ev == nil {
				// Unsubscription done, stop listening.
				dlEventCh = nil
				break events
			}
			switch ev.Data.(type) {
			case downloader.StartEvent:
				c.syncing.Store(true)

			case downloader.FailedEvent:
				c.syncing.Store(false)

			case downloader.DoneEvent:
				c.syncing.Store(false)

				// Stop reacting to downloader events.
				downloaderEvents.Unsubscribe()
			}
		}
	}
}

// WithTxPool initializes transaction pool API for DBFT interactions with memory pool
// (fetching unknown transactions).
func (c *DBFT) WithTxPool(pool txPool) {
	c.txpool = pool
}

// WithLegacyPool initializes transaction legacy pool API for ValidateDecryptedTx
func (c *DBFT) WithLegacyPool(pool legacyPool) {
	c.legacypool = pool
}

// postBlock is a callback that updates latest accepted block data and resets
// last proposal data. It must be called every time new block arrives from chain
// or from consensus.
func (c *DBFT) postBlock(h *types.Header) {
	num := h.Number.Uint64()
	if c.lastIndex < num {
		c.lastTimestamp = h.Time
		c.lastIndex = h.Number.Uint64()
		c.lastBlockHash = h.Hash()
		c.lastBlockSealHash = HonestSealHash(h)
		c.lastBlockExtra = h.Extra

		// handle DKG
		if c.lastIndex >= uint64(c.config.dkgEnablingHeight) {
			err := c.handleDKG(h)
			if err != nil {
				log.Error("handleDKG error", "height", num, "err", err)
			}
		}
	}
}

// Author implements consensus.Engine, returning the Ethereum address recovered
// from the header's Coinbase. Engine API expects Author to be the address to send
// reward for in-block transactions to, and thus, in common case Author differs
// from the Primary node that actually was the block's author.
func (c *DBFT) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

// Primary returns the Ethereum address of block's Primary validator. It's the node
// that was the author of accepted block's proposal. For beneficiary (in-block
// transactions reward receiver) please, refer to Author. This method expects block's
// Extra to be properly filled with at least a set of validators for the corresponding
// consensus round.
func (c *DBFT) Primary(header *types.Header) (common.Address, error) {
	vals, _, err := getSignersAndSigs(c.config, header.Extra)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	return vals[header.Primary()], nil
}

// Signers returns the set of Ethereum consensus node addresses that committed the
// given header. Note that for the block of same height there might be different
// set of signers returned by different nodes depending on the set of block's signatures.
func (c *DBFT) Signers(header *types.Header) ([]common.Address, error) {
	_, sigs, err := getSignersAndSigs(c.config, header.Extra)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	var (
		signers = make([]common.Address, len(sigs))
		h       = HonestSealHash(header).Bytes()
	)
	for i := range sigs {
		pubkey, err := crypto.Ecrecover(h, sigs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to recover signer from signature %d: %w", i, err)
		}
		signers[i] = crypto.PubkeyBytesToAddress(pubkey)
	}
	return signers, nil
}

// Validators returns the set of Ethereum consensus node addresses that are validators
// of the given header. Note that for the block of same height the set of validators is
// always the same.
func (c *DBFT) Validators(header *types.Header) ([]common.Address, error) {
	vals, _, err := getSignersAndSigs(c.config, header.Extra)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	return vals, nil
}

// VerifyHeader checks whether a header conforms to the consensus rules.
func (c *DBFT) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	return c.verifyHeader(chain, header, nil, true)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers. The
// method returns a quit channel to abort the operations and a results channel to
// retrieve the async verifications (the order is that of the input slice).
func (c *DBFT) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := c.verifyHeader(chain, header, headers[:i], true)

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.
func (c *DBFT) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header, isSealed bool) error {
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()

	// Don't waste time checking blocks from the future.
	if isSealed && header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}

	// Nonces contain Primary index, so it's not required for them to be 0x00..0
	// ([nonceAuthVote]) or 0xff..f ([nonceDropVote]), thus, skip Nonce check.
	// It's not bound to checkpoint anymore.

	// Check that the extra-data contains both the vanity and signature
	if len(header.Extra) < dbftutil.ExtraVersionLen {
		return errMissingVanity
	}
	if header.Extra[0] != dbftutil.ExtraV0 {
		return fmt.Errorf("unknown Extra version: %d", header.Extra[0])
	}
	if isSealed {
		m := crypto.GetBFTHonestNodeCount(len(c.config.StandByValidators))
		sigBytesLen := m * extraSeal
		if len(header.Extra) < dbftutil.ExtraVersionLen+sigBytesLen {
			return errMissingSignature
		}
		// Ensure that the extra-data contains validators list.
		signersBytes := len(header.Extra) - dbftutil.ExtraVersionLen - sigBytesLen
		if signersBytes == 0 {
			return fmt.Errorf("missing validators addresses")
		}
		if signersBytes%common.AddressLength != 0 {
			return errInvalidExtraSigners
		}

		// Ensure that the mix digest is not zero.
		if header.MixDigest == (common.Hash{}) {
			return errInvalidMixDigest
		}
	}
	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}
	// Ensure that the block's difficulty is meaningful (may not be correct at this point)
	if number > 0 && isSealed {
		if header.Difficulty == nil || (header.Difficulty.Cmp(diffInTurn) != 0 && header.Difficulty.Cmp(diffNoTurn) != 0) {
			return errInvalidDifficulty
		}
	}
	// Verify that the gas limit is <= 2^63-1
	if header.GasLimit > params.MaxGasLimit {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, params.MaxGasLimit)
	}
	shanghai := chain.Config().IsShanghai(header.Number, header.Time)
	if shanghai {
		if header.WithdrawalsHash == nil {
			return errors.New("missing withdrawalsHash")
		}
		// For now, only empty withdrawals are supported. Once non-empty withdrawals are allowed,
		// we need to ensure that withdrawals hash matches withdrawals, it's done at the level of
		// WithVerifyBlock dBFT callback during the consensus process and at the blockchain level
		// for real (accepted) blocks.
		if header.WithdrawalsHash.Cmp(types.EmptyWithdrawalsHash) != 0 {
			return errors.New("dBFT supports only empty withdrawals")
		}
	}
	if !shanghai && header.WithdrawalsHash != nil {
		return fmt.Errorf("invalid withdrawalsHash: have %x, expected nil", header.WithdrawalsHash)
	}
	if chain.Config().IsCancun(header.Number, header.Time) {
		return errors.New("dbft does not support cancun fork")
	}
	// All basic checks passed, verify cascading fields
	return c.verifyCascadingFields(chain, header, parents, isSealed)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (c *DBFT) verifyCascadingFields(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header, isSealed bool) error {
	// The genesis block is the always valid dead-end
	number := header.Number.Uint64()
	if number == 0 {
		return nil
	}
	// Ensure that the block's timestamp isn't too close to its parent
	var parent *types.Header
	if len(parents) > 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	if parent == nil ||
		parent.Number.Uint64() != number-1 ||
		(isSealed && parent.Hash() != header.ParentHash) {
		return consensus.ErrUnknownAncestor
	}
	if parent.Time > header.Time {
		return errInvalidTimestamp
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	if !chain.Config().IsLondon(header.Number) {
		// Verify BaseFee not present before EIP-1559 fork.
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, want <nil>", header.BaseFee)
		}
		if err := misc.VerifyGaslimit(parent.GasLimit, header.GasLimit); err != nil {
			return err
		}
	} else if err := eip1559.VerifyEIP1559Header(chain.Config(), parent, header); err != nil {
		// Verify the header's EIP-1559 attributes.
		return err
	}
	// This check forces Coinbase to be the same among all consensus nodes.
	if header.Coinbase != c.config.Coinbase {
		return fmt.Errorf("invalid Coinbase: expected %s, got %s", c.config.Coinbase, header.Coinbase)
	}

	// All basic checks passed, verify the seal and return.
	if !isSealed {
		return nil
	}
	return c.verifySeal(header, parents, parent)
}

// VerifyUncles implements consensus.Engine, always returning an error for any
// uncles as this consensus mechanism doesn't permit uncles.
func (c *DBFT) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

// verifySeal checks whether the signature contained in the header satisfies the
// consensus protocol requirements. The method accepts an optional list of parent
// headers that aren't yet part of the local blockchain to generate the snapshots
// from.
func (c *DBFT) verifySeal(header *types.Header, parents []*types.Header, parent *types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	_, err := c.verifyExtra(HonestSealHash(header), header.Extra, parent.NextConsensus())
	if err != nil {
		return fmt.Errorf("invalid Extra: %w", err)
	}

	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !c.fakeDiff {
		inturn := c.inturn(header.Primary(), header.Number.Uint64())
		if inturn && header.Difficulty.Cmp(diffInTurn) != 0 {
			return errWrongDifficulty
		}
		if !inturn && header.Difficulty.Cmp(diffNoTurn) != 0 {
			return errWrongDifficulty
		}
	}
	return nil
}

// inturn returns whether specified consensus node was the first designated dBFT
// speaker for the specified block height.
func (c *DBFT) inturn(validatorIndex byte, blockNum uint64) bool {
	return blockNum%uint64(len(c.config.StandByValidators)) == uint64(validatorIndex)
}

func (c *DBFT) verifyExtra(sealHash common.Hash, extra []byte, parentNextConsensus common.Hash) ([]common.Address, error) {
	// Resolve the authorization key and check against signers.
	vals, sigs, err := getSignersAndSigs(c.config, extra)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve validators and signatures from header: %w", err)
	}
	nextConsensus := dbftutil.GetNextConsensusHash(vals)
	if parentNextConsensus != nextConsensus {
		return nil, fmt.Errorf("invalid NextConsensus retrieved from validators addresses: expected %s, got %s", parentNextConsensus, nextConsensus)
	}
	err = crypto.VerifyMultiBFT(sealHash.Bytes(), vals, sigs)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errUnauthorizedSigner, err)
	}
	return vals, nil
}

// Prepare implements consensus.Engine, preparing all the consensus fields of the
// header for running the transactions on top.
func (c *DBFT) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	// Nonce (primary index) will be filled during consensus process, leave it empty
	// for now.
	header.Nonce = types.BlockNonce{}

	// Use in-turn difficulty as a base for miner's transactions processing to skip dBFT transactions reprocessing in best
	// case. In worse case this field will be overridden during further dBFT consensus in case of CV and state will be
	// recalculated by dBFT based on new value.
	header.Difficulty = new(big.Int).Set(diffInTurn)

	// Ensure the extra data has all its components. Set default Extra version if not
	// provided by miner.
	if len(header.Extra) < dbftutil.ExtraVersionLen {
		header.Extra = []byte{dbftutil.ExtraV0}
	}
	// Fill only Extra version. The rest components of Header's Extra (validators
	// addresses and BFT number of validators signatures) are treated as changeable
	// and are not filled in during Prepare. These data will be set after block
	// sealing in processBlock dBFT callback.
	header.Extra = header.Extra[:dbftutil.ExtraVersionLen]

	// Mix digest is reserved for now, set to empty
	header.MixDigest = common.Hash{}

	// Ensure the timestamp has the correct delay
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Time = uint64(time.Now().Unix())
	return nil
}

// Finalize implements consensus.Engine. For now, it only manages block withdrawals.
func (c *DBFT) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, withdrawals []*types.Withdrawal) {
	// Withdrawals processing.
	for _, w := range withdrawals {
		// Convert amount from gwei to wei.
		amount := new(uint256.Int).SetUint64(w.Amount)
		amount = amount.Mul(amount, uint256.NewInt(params.GWei))
		state.AddBalance(w.Address, amount)
	}
	// No block rewards in PoA, so the state remains as is
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (c *DBFT) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt, withdrawals []*types.Withdrawal) (*types.Block, error) {
	shanghai := chain.Config().IsShanghai(header.Number, header.Time)
	if shanghai {
		// All blocks after Shanghai must include a withdrawals root.
		if withdrawals == nil {
			withdrawals = make([]*types.Withdrawal, 0)
		}
	} else {
		if len(withdrawals) > 0 {
			return nil, errors.New("withdrawals set before Shanghai activation")
		}
	}

	// Finalize block
	c.Finalize(chain, header, state, txs, uncles, withdrawals)

	// Assign the final state root to header.
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Assemble and return the final block for sealing.
	b := types.NewBlockWithWithdrawals(header, txs, nil, receipts, withdrawals, trie.NewStackTrie(nil))
	return b, nil
}

// Authorize injects a private key into the consensus engine to mint new blocks
// with.
func (c *DBFT) Authorize(signer common.Address, signFn SignerFn, amevKeystore *antimev.KeyStore) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.signer = signer
	c.signFn = signFn
	c.amevKeystore = amevKeystore
}

// Start initializes last block cache, fetches fresh proposal from miner, starts
// DBFT engine event loop and starts dBFT consensus process.
func (c *DBFT) Start(chain ChainHeaderWriter) {
	if c.dbftStarted.CompareAndSwap(false, true) {
		c.chain = chain
		c.blockQueue.chain = chain

		// Current head of the header chain may be above the block chain, and
		// dBFT must always be based on the latest state data (i.e. blocks), thus,
		// retrieve current chain header to initialize context and wait until chain
		// will recover and process blocks up to the known and most fresh header.
		currHeader := chain.CurrentHeader()
		c.lastIndex = currHeader.Number.Uint64()
		c.lastTimestamp = currHeader.Time
		c.lastBlockHash = currHeader.Hash()
		c.lastBlockSealHash = HonestSealHash(currHeader)
		c.lastBlockExtra = currHeader.Extra

		// Before consensus start we should wait for initial sealing proposal to be
		// initialised by miner. Start consensus once we have new sealing work in Seal.
		err := c.waitForNewSealingProposal(c.lastIndex+1, false)
		if err != nil {
			log.Warn("Failed to fetch latest sealing proposal",
				"index", c.lastIndex+1,
				"err", err.Error())
			return
		}

		log.Info("Starting dBFT engine",
			"last height", c.lastIndex,
			"last timestamp", c.lastTimestamp)
		c.dbft.Start(c.lastTimestamp * NsInS)

		// Subscribe for minted blocks.
		c.chainHeadSub = c.chain.SubscribeChainHeadEvent(c.chainHeadEvents)

		go c.eventLoop()
	}
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// the local signing credentials.
func (c *DBFT) Seal(chain consensus.ChainHeaderReader, b *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	// Coinbase must be configured by miner and fetched from the node's config, do not change it.
	if b.Coinbase().Cmp(common.Address{}) == 0 {
		return errors.New("unexpected empty Coinbase in sealing task")
	}

	// Save proposal so that the most fresh data will be available for dBFT once
	// the new block should be created.
	c.lastProposalLock.Lock()
	c.lastProposal = b
	c.lastProposalLock.Unlock()

	return nil
}

// waitForNewSealingProposal allows background lastProposal update and wait for
// new suitable proposal for the desired height (or upper).
func (c *DBFT) waitForNewSealingProposal(desiredHeight uint64, updateContext bool) error {
	log.Info("Fetching latest sealing proposal",
		"desired number", desiredHeight)
	var (
		ok           bool
		lastProposal *types.Block
	)
	// Wait here...
	for {
		c.lastProposalLock.RLock()
		if c.lastProposal != nil && c.lastProposal.NumberU64() >= desiredHeight {
			lastProposal = c.lastProposal
			ok = true
		}
		c.lastProposalLock.RUnlock()
		if ok {
			break
		}
		select {
		case <-c.quit:
			return errors.New("shutdown detected")
		default:
		}
		time.Sleep(time.Second)
	}

	// And then retrieve proposal and check it.
	b := lastProposal
	if b.NumberU64() > c.lastIndex+1 {
		log.Info("New chain segment detected",
			"dBFT latest block index", c.lastIndex,
			"sealing proposal index", b.NumberU64())
		ltstHeader := c.chain.GetHeaderByNumber(b.NumberU64() - 1)
		c.postBlock(ltstHeader)
	}

	if b.ParentHash().Cmp(c.lastBlockHash) != 0 {
		// In case of chain reorg it may happen that DBFT last block cache stores
		// outdated parent hash and Extra, thus, if the rest of new parent information
		// is valid, then use it to construct new sealing proposal.
		parent := c.chain.GetHeaderByHash(b.ParentHash())
		if parent == nil {
			return fmt.Errorf("can't verify sealing task: failed to get parent from chain: expected %s, got %s", c.lastBlockHash, b.ParentHash())
		}
		if actual := HonestSealHash(parent); c.lastBlockSealHash != actual {
			return fmt.Errorf("invalid sealing task: invalid Parent honest seal hash: expected %s, got %s", c.lastBlockSealHash, actual)
		}
		log.Info("Update cached dBFT last block information",
			"number", c.lastIndex,
			"old hash", c.lastBlockHash,
			"new hash", b.ParentHash(),
			"seal hash", c.lastBlockSealHash)
		c.lastBlockHash = b.ParentHash()
		c.lastBlockExtra = parent.Extra
	}

	c.sealingProposal = lastProposal.Header()
	c.sealingTransactions = lastProposal.Transactions()
	log.Info("Sealing proposal updated",
		"number", c.sealingProposal.Number,
		"sealhash", c.SealHash(c.sealingProposal),
		"parent hash", c.sealingProposal.ParentHash,
		"txs", len(c.sealingTransactions))

	if updateContext {
		// dBFT can't update its PrevHash in the middle of consensus process, thus,
		// update it manually to keep it in sync with the actual last block hash in
		// case of chain reorgs (it's thread-safe to perform it here, because eventLoop
		// is waiting for the end of Seal in this case).
		c.dbft.Context.PrevHash = c.lastBlockHash
	}

	return nil
}

func (c *DBFT) eventLoop() {
	c.eventLoopStarted.Store(true)
	log.Info("dBFT event loop started")

	// Track of the downloader events to be in sync with miner's status since miner's
	// work is suspended during the initial chain sync. Please be aware that this is
	// a one shot type of update loop. It's entered once and as soon as `Done` has
	// been broadcasted the events are unregistered and the loop is exited. This to
	// prevent a major security vuln where external parties can DOS you with blocks
	// and halt your dBFT operation for as long as the DOS continues.
	downloaderEvents := c.mux.Subscribe(downloader.DoneEvent{}, downloader.FailedEvent{})
	defer func() {
		if !downloaderEvents.Closed() {
			downloaderEvents.Unsubscribe()
		}
	}()
	dlEventCh := downloaderEvents.Chan()

events:
	for {
		oldView := c.dbft.ViewNumber
		select {
		case <-c.quit:
			log.Info("shutting down dBFT event loop")
			c.dbft.Timer.Stop()

			c.chainHeadSub.Unsubscribe()
			break events
		case <-c.dbft.Timer.C():
			h, v := c.dbft.Timer.Height(), c.dbft.Timer.View()
			log.Debug("timer fired",
				"height", h,
				"view", uint(v))
			c.dbft.OnTimeout(h, v)
		case msg := <-c.messages:
			fields := []any{
				"from", msg.message.ValidatorIndex,
				"type", msg.Type().String(),
			}

			if msg.Type() == dbft.RecoveryMessageType {
				rec := msg.GetRecoveryMessage().(*recoveryMessage)
				if rec.PreparationHashExt == nil {
					req := rec.GetPrepareRequest(&msg, c.dbft.Validators, uint16(c.dbft.PrimaryIndex))
					if req != nil {
						h := req.Hash()
						rec.PreparationHashExt = &h
					}
				}

				fields = append(fields,
					"#preparation", len(rec.PreparationPayloads),
					"#commit", len(rec.CommitPayloads),
					"#changeview", len(rec.ChangeViewPayloads),
					"#request", rec.PrepareRequest != nil,
					"#hash", rec.PreparationHashExt != nil)
			}
			log.Debug("received message", fields...)
			c.dbft.OnReceive(&msg)
		case tx := <-c.txs:
			c.dbft.OnTransaction(&Transaction{Tx: tx})
		case b := <-c.chainHeadEvents:
			err := c.handleChainBlock(b.Block.Header(), true)
			if err != nil {
				log.Warn("Failed to handle chain block",
					"index", b.Block.NumberU64(),
					"err", err.Error())
				break events
			}
		case err := <-c.chainHeadSub.Err():
			// System has stopped.
			log.Info("Stopping dBFT service since block subscriptions are stopped")
			if err != nil {
				log.Info("Block subscriptions error",
					"error", err.Error())
			}
			break events
		case ev := <-dlEventCh:
			if ev == nil {
				// Unsubscription done, stop listening.
				dlEventCh = nil
				continue
			}
			switch ev.Data.(type) {
			case downloader.FailedEvent:
				latest := c.chain.CurrentHeader()
				err := c.handleChainBlock(latest, false)
				if err != nil {
					log.Warn("Failed to handle latest chain block",
						"index", latest.Number.Uint64(),
						"err", err.Error())
					break events
				}

			case downloader.DoneEvent:
				// Stop reacting to downloader events.
				downloaderEvents.Unsubscribe()

				latest := c.chain.CurrentHeader()
				err := c.handleChainBlock(latest, false)
				if err != nil {
					log.Warn("Failed to handle latest chain block",
						"index", latest.Number.Uint64(),
						"err", err.Error())
					break events
				}
			}
		}
		// Always process block event if there is any, we can add one above or external
		// services can add several blocks during message processing.
		var latestBlock core.ChainHeadEvent
	syncLoop:
		for {
			select {
			case latestBlock = <-c.chainHeadEvents:
			default:
				break syncLoop
			}
		}
		if latestBlock.Block != nil {
			err := c.handleChainBlock(latestBlock.Block.Header(), true)
			if err != nil {
				log.Warn("Failed to handle latest chain block",
					"index", latestBlock.Block.NumberU64(),
					"err", err.Error())
				break events
			}
		}
		newView := c.dbft.ViewNumber
		// If ChangeView has happened, we always need to wait for the new proposal
		// from miner.
		if newView > oldView {
			log.Info("Change view detected, waiting for new sealing task to be submitted by miner", "old view", oldView, "new view", newView)
			err := c.waitForNewSealingProposal(uint64(c.dbft.Context.BlockIndex), true)
			if err != nil {
				log.Warn("Failed to fetch latest sealing proposal",
					"index", c.dbft.Context.BlockIndex,
					"err", err.Error())
				break events
			}
			log.Info("Start dBFT process for updated sealing work",
				"index", c.dbft.Context.BlockIndex,
				"view", newView,
			)
		}
	}
drainLoop:
	for {
		select {
		case <-c.messages:
		case <-c.txs:
		case <-c.chainHeadEvents:
		default:
			break drainLoop
		}
	}
	close(c.messages)
	close(c.txs)
	close(c.chainHeadEvents)
	close(c.finished)
	log.Info("dBFT event loop finished")
}

// OnPayload handles Payload receive.
func (c *DBFT) OnPayload(cp *dbftproto.Message) error {
	if c.dbft == nil || !c.dbftStarted.Load() {
		log.Debug("Skip dBFT payload handling: dbft is inactive or not started yet", "hash", cp.Hash())
		return nil
	}
	if c.syncing.Load() {
		log.Debug("Skip dBFT payload handling due to sync", "hash", cp.Hash())
		return nil
	}

	p := payloadFromMessage(cp)
	// decode payload data into message
	if err := p.decodeData(); err != nil {
		log.Info("can't decode payload data", "hash", cp.Hash(), "error", err)
		return nil
	}

	if err := c.validatePayload(p); err != nil {
		log.Info("Can't validate payload", "hash", cp.Hash(), "err", err)
		return nil
	}

	c.messages <- *p
	return nil
}

// OnTransaction is a dBFT callback that reacts on new incoming transaction arrived
// via P2P. It's important to call this callback on every incoming transaction
// skipping mempool filtering for proper dBFT functioning.
func (c *DBFT) OnTransaction(txs []*types.Transaction) {
	if c.dbft == nil || !c.dbftStarted.Load() {
		log.Debug("Skip txs batch handling: dbft is inactive or not started yet")
		return
	}

	var cbList = c.txCbList.Load()
	if cbList != nil {
		for _, tx := range txs {
			_, found := slices.BinarySearchFunc(cbList.([]common.Hash), tx.Hash(), common.Hash.Cmp)
			if found {
				c.txs <- tx
			}
		}
	}
}

func payloadFromMessage(ep *dbftproto.Message) *Payload {
	return &Payload{
		Message: *ep,
		message: message{},
	}
}

func (c *DBFT) validatePayload(p *Payload) error {
	h := c.chain.CurrentBlock().Number.Uint64()
	validators, err := c.getValidators(&h, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get next block validators: %w", err)
	}
	if int(p.message.ValidatorIndex) >= len(validators) {
		return fmt.Errorf("invalid message validator index: validators count is %d, requested %d", len(validators), p.message.ValidatorIndex)
	}

	val := validators[p.message.ValidatorIndex]
	if p.Sender != val {
		return fmt.Errorf("message sender is not a validator: expected %s, got %s", val, p.Sender)
	}

	return nil
}

// IsExtensibleAllowed determines if address is allowed to send extensible payloads
// (only consensus payloads for now) at the specified height.
func (c *DBFT) IsExtensibleAllowed(h uint64, u common.Address) error {
	// Can't verify extensible sender if the node has an outdated state.
	if c.syncing.Load() {
		return dbftproto.ErrSyncing
	}
	// Only validators are included into extensible whitelist for now.
	validators, err := c.getValidators(&h, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get validators: %w", err)
	}
	_, found := slices.BinarySearchFunc(validators, u, common.Address.Cmp)
	if !found {
		return fmt.Errorf("address is not a validator")
	}
	return nil
}

func (c *DBFT) newPayload(ctx *dbft.Context[common.Hash], t dbft.MessageType, msg any) dbft.ConsensusPayload[common.Hash] {
	var cp = new(Payload)
	cp.BlockIndex = uint64(ctx.BlockIndex)
	cp.message.ValidatorIndex = byte(ctx.MyIndex)
	cp.message.ViewNumber = ctx.ViewNumber
	cp.message.Type = messageType(t)
	cp.msgPayload = msg

	cp.Message.ValidBlockStart = 0
	cp.Message.ValidBlockEnd = uint64(ctx.BlockIndex)
	cp.Message.Sender = ctx.Validators[ctx.MyIndex].(*PublicKey).Account

	return cp
}

func (c *DBFT) handleChainBlock(h *types.Header, checkForSync bool) error {
	// A short path if miner is not active and the node is in the process of block
	// sync. In this case dBFT can't react properly on the newcoming blocks since no
	// sealing task is expected from miner.
	if checkForSync && c.syncing.Load() {
		log.Info("Skipping dBFT block callback due to sync",
			"block index", h.Number.Int64(),
			"dbft index", c.dbft.BlockIndex,
		)
		return nil
	}

	// We can get our own block here, so check for index.
	if uint32(h.Number.Uint64()) >= c.dbft.BlockIndex {
		log.Info("New block in the chain",
			"dbft index", c.dbft.BlockIndex,
			"chain index", c.chain.CurrentBlock().Number.Uint64(),
			"hash", h.Hash().String(),
			"parent hash", h.ParentHash.String(),
			"primary", h.Primary(),
			"coinbase", h.Coinbase,
			"mix digest", h.MixDigest.String())
		c.postBlock(h)

		err := c.waitForNewSealingProposal(c.lastIndex+1, false)
		if err != nil {
			log.Warn("Failed to fetch latest sealing proposal",
				"index", c.lastIndex+1,
				"err", err.Error())
			return err
		}
		c.dbft.Reset(c.lastTimestamp * NsInS)
	}
	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
// that a new block should have assuming that the authorized node will be a speaker
// in the consensus round when the new block is accepted. And thus, the returned
// value may be different from the actual new block difficulty. The difficulty
// adjustment algorithm is the following:
// * DIFF_NOTURN(2) if BLOCK_NUMBER % SIGNER_COUNT != SIGNER_INDEX
// * DIFF_INTURN(1) if BLOCK_NUMBER % SIGNER_COUNT == SIGNER_INDEX
func (c *DBFT) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	c.lock.RLock()
	signer := c.signer
	c.lock.RUnlock()
	return c.calcDifficulty(signer, parent)
}

func (c *DBFT) calcDifficulty(signer common.Address, parent *types.Header) *big.Int {
	h := parent.Number.Uint64()
	vals, err := c.getValidators(&h, nil, nil)
	if err != nil {
		return nil
	}
	var signerIdx = -1
	for i, v := range vals {
		if v == signer {
			signerIdx = i
			break
		}
	}
	return c.getDifficulty(signerIdx, parent.Number.Uint64()+1)
}

func (c *DBFT) getDifficulty(signerIdx int, blockNum uint64) *big.Int {
	if signerIdx != -1 && c.inturn(byte(signerIdx), blockNum) {
		return new(big.Int).Set(diffInTurn)
	}
	return new(big.Int).Set(diffNoTurn)
}

// SealHash returns the hash of a block prior to it being sealed. It implements
// consensus.Engine interface.
func (c *DBFT) SealHash(header *types.Header) common.Hash {
	return WorkerSealHash(header)
}

// WorkerSealHash returns the hash of a header prior to it being sealed. WorkerSealHash is
// override to exclude those header fields that will be changed by dBFT during
// block sealing: MixDigest, Nonce and last [crypto.SignatureLength] bytes of
// Extra.
//
// Be careful no to use WorkerSealHash anywhere where "the honest" WorkerSealHash is required.
func WorkerSealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()
	encodeUnchangeableHeader(hasher, header)
	hasher.(crypto.KeccakState).Read(hash[:])
	return hash
}

// encodeUnchangeableHeader encodes those header fields that won't be changed by
// dBFT during block sealing: every header field except MixDigest, Nonce
// and last [crypto.SignatureLength] bytes of Extra.
func encodeUnchangeableHeader(w io.Writer, header *types.Header) {
	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		// Do not include validators addresses into hashable part.
		header.Extra[:dbftutil.ExtraVersionLen], // Yes, this will panic if extra is too short.
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		enc = append(enc, header.WithdrawalsHash)
	}
	if err := rlp.Encode(w, enc); err != nil {
		panic("can't encode: " + err.Error())
	}
}

// HonestSealHash returns the hash of a block prior to it being sealed. It differs
// from WorkerSealHash in that all block fields except Extra's signature bytes are being
// hashed.
func HonestSealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()
	encodeSigHeader(hasher, header)
	hasher.(crypto.KeccakState).Read(hash[:])
	return hash
}

// dbftRLP returns the rlp bytes which needs to be signed for the proof-of-authority
// sealing. The RLP to sign consists of the entire header apart from the 65 byte signature
// contained at the end of the extra data.
//
// Note, the method requires the extra data to be at least 65 bytes, otherwise it
// panics. This is done to avoid accidentally using both forms (signature present
// or not), which could be abused to produce different hashes for the same header.
func dbftRLP(header *types.Header) []byte {
	b := new(bytes.Buffer)
	encodeSigHeader(b, header)
	return b.Bytes()
}

func encodeSigHeader(w io.Writer, header *types.Header) {
	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra[:dbftutil.ExtraVersionLen], // Yes, this will panic if extra is too short
		header.MixDigest,
		header.Nonce,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		enc = append(enc, header.WithdrawalsHash)
	}
	if err := rlp.Encode(w, enc); err != nil {
		panic("can't encode: " + err.Error())
	}
}

// Close implements consensus.Engine.
func (c *DBFT) Close() error {
	if c.dbftStarted.CompareAndSwap(true, false) {
		log.Info("Shutting down dBFT engine")
		close(c.quit)
		if c.eventLoopStarted.CompareAndSwap(true, false) {
			<-c.finished
		}
	}
	log.Info("dBFT engine stopped")
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (c *DBFT) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{{
		Namespace: "dbft",
		Service:   &API{chain: chain, bft: c},
	}}
}

// getValidators returns validators chosen in the result of the latest
// finalized voting epoch. It calls Governance contract under the hood. The call
// is based on the provided state or (if not provided) on the state of the block
// with the specified height. Validators returned from this method are always
// sorted by bytes order (even if the list returned from governance contract is
// sorted in another way). This method uses cached values in case of validators
// requested by block height.
func (c *DBFT) getValidators(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	res, err := c.getOriginalValidators(blockNum, state, header)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(res, common.Address.Cmp)
	return res, err
}

func (c *DBFT) shouldUpdateCommitteeAt(blockNum uint64) bool {
	return blockNum%uint64(len(c.config.StandByValidators)) == 0
}

// getOriginalValidators returns validators chosen in the result of the latest
// finalized voting epoch. It calls Governance contract under the hood. The call
// is based on the provided state or (if not provided) on the state of the block
// with the specified height. Validators returned from this method are not
// sorted with original order from Governance contract. This method uses cached values in case of validators
// requested by block height.
func (c *DBFT) getOriginalValidators(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	if c.ethAPI == nil {
		return nil, errors.New("eth blockchain API is not initialized, dBFT can't function properly")
	}

	if state == nil && blockNum != nil {
		vals, ok := c.validatorsCache.Get(*blockNum)
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
	var result hexutil.Bytes
	if state != nil {
		result, err = c.ethAPI.CallAtState(ctx, args, state, header)
	} else if blockNum != nil {
		blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(*blockNum))
		result, err = c.ethAPI.Call(ctx, args, &blockNr, nil, nil)
	} else {
		return nil, fmt.Errorf("failed to compute validators: both block number and state are nil")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to perform '%s' call: %w", method, err)
	}

	var res []common.Address
	err = systemcontracts.GovernanceABI.UnpackIntoInterface(&res, method, result)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack validators: %w", err)
	}

	// Update cache in case if existing state was used for validators retrieval.
	if state == nil && blockNum != nil {
		_ = c.validatorsCache.Add(*blockNum, res)
	}

	return res, err
}

// getDKGIndex returns validator dkg index (original validator index +1) by validatorIndex (ordered validator index).
func (c *DBFT) getDKGIndex(validatorIndex int, blockNum uint64) (int, error) {
	originValidators, err := c.getOriginalValidators(&blockNum, nil, nil)
	if err != nil {
		return -1, err
	}
	if validatorIndex < 0 || validatorIndex >= len(originValidators) {
		return -1, fmt.Errorf("invalid validator index: validators count is %d, requested %d", len(originValidators), validatorIndex)
	}
	orderedValidators := make([]common.Address, len(originValidators))
	copy(orderedValidators, originValidators)
	slices.SortFunc(orderedValidators, common.Address.Cmp)
	addr := orderedValidators[validatorIndex]
	for i := range originValidators {
		if orderedValidators[i] == addr {
			return i + 1, nil
		}
	}
	// impossible case
	return -1, nil
}
