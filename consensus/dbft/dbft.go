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

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/accounts/abi"
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
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
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

	bigOne = big.NewInt(1)

	emptyWithdrawals = make([]*types.Withdrawal, 0)
)

const (
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
	// crossEpochDecryptionStartRound is the number of DKG round (as denoted in KeyManagement
	// system contract) starting from which continuous cross-epoch Envelopes decryption is supported.
	// First DKG round setups sharing key, second DKG round setups resharing key, hence resharing
	// key may be used for decryption starting from the third round.
	crossEpochDecryptionStartRound = 3
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
	// 1 byte, which is required to store the extra version.
	errMissingVanity = errors.New("extra-data 1-byte version prefix missing")

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
	lastTimestamp uint64 // in seconds, like Eth requires.
	lastIndex     uint64
	lastBlockHash common.Hash
	// lastBlockSealHash is a honest seal hash of the last block received either from
	// dBFT or from chain. It holds bytes of either Kechaak256 hash (in case if
	// multisignature is used for block signing) or G2 point on BLS12381 (in case if
	// threshold signature is used for block signing).
	lastBlockSealHash []byte
	lastBlockExtra    dbftutil.Extra

	// lastProposal holds the latest proposal submitted to dBFT by miner. It is updated
	// irrespectively and concurrently to dBFT process, thus, access should be protected
	// by mutex.
	lastProposalLock sync.RWMutex
	lastProposal     *types.Block

	// sealingProposal holds current proposal dBFT is working on. It's not protected by
	// mutex since every access point is controlled by eventLoop, thus, not concurrent.
	sealingProposal     *types.Header
	sealingTransactions types.Transactions

	// sealingState, sealingBlock and sealingReceipts are Primary-only set of fields got
	// after sealingProposal construction and processing. These fields are not protected
	// by mutex since every access point is controlled by eventLoop. These fields must
	// not be accessed if the node is a backup.
	sealingState    *state.StateDB
	sealingBlock    *types.Block
	sealingReceipts types.Receipts

	// chain and mempool instances needed for proper dBFT callbacks functioning.
	chain  ChainHeaderReader
	txpool txPool

	// signerConfig is a types.Signer used to retrieve transactions signer
	// irrespectively of the chain's height.
	signerConfig types.Signer

	quit     chan struct{}
	finished chan struct{}

	// various native contract APIs that dBFT uses.
	backend         *ethapi.Backend
	txAPI           *ethapi.TransactionAPI
	validatorsCache *lru.Cache[uint64, []common.Address]

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications

	// The fields for dkg
	dkgSnapshot  *Snapshot
	txWatchList  []*TxWatchRetry
	loopTaskChan chan *TxWatchList
}

// config represents Engine configuration.
type config struct {
	*params.DBFTConfig
	dkgEnablingHeight      int64
	antiMEVEnablingHeight  int64
	enforceECDSASignatures bool
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
		signerConfig:    types.LatestSigner(chainCfg),

		quit:     make(chan struct{}),
		finished: make(chan struct{}),

		validatorsCache: lru.NewCache[uint64, []common.Address](validatorsCacheCap),

		dkgSnapshot:  NewSnapshot(),
		loopTaskChan: make(chan *TxWatchList),
	}

	var err error
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dBFT logger: %w", err)
	}
	dbftCfg := []func(*dbft.Config[common.Hash]){
		dbft.WithTimer[common.Hash](timer.New()),
		dbft.WithLogger[common.Hash](logger),
		dbft.WithSecondsPerBlock[common.Hash](time.Duration(bftCfg.SecondsPerBlock) * time.Second),
		dbft.WithGetKeyPair[common.Hash](c.getKeyPairCb),
		dbft.WithCurrentHeight[common.Hash](c.currentHeightCb),
		dbft.WithCurrentBlockHash[common.Hash](c.currentBlockHashCb),
		dbft.WithGetValidators[common.Hash](c.getValidatorsCb),
		dbft.WithProcessBlock[common.Hash](c.processBlockCb),
		dbft.WithNewBlockFromContext[common.Hash](c.newBlockFromContextCb),
		dbft.WithWatchOnly[common.Hash](func() bool { return false }),
		dbft.WithGetTx[common.Hash](c.getTxCb),
		dbft.WithGetVerified[common.Hash](c.getVerifiedCb),
		dbft.WithRequestTx[common.Hash](c.requestTxCb),
		dbft.WithStopTxFlow[common.Hash](c.stopTxFlowCb),
		dbft.WithNewConsensusPayload[common.Hash](c.newConsensusPayloadCb),
		dbft.WithNewPrepareRequest[common.Hash](c.newPrepareRequestCb),
		dbft.WithNewCommit[common.Hash](c.newCommitCb),
		dbft.WithNewPrepareResponse[common.Hash](c.newPrepareResponseCb),
		dbft.WithNewChangeView[common.Hash](c.newChangeViewCb),
		dbft.WithNewRecoveryRequest[common.Hash](c.newRecoveryRequestCb),
		dbft.WithNewRecoveryMessage[common.Hash](c.newRecoveryMessageCb),
		dbft.WithVerifyPrepareResponse[common.Hash](func(_ dbft.ConsensusPayload[common.Hash]) error { return nil }),
		dbft.WithVerifyCommit[common.Hash](c.verifyCommitCb),
		dbft.WithVerifyPrepareRequest[common.Hash](c.verifyPrepareRequestCb),
		dbft.WithVerifyBlock[common.Hash](c.verifyBlockCb),
		dbft.WithBroadcast[common.Hash](c.broadcastCb),
		dbft.WithAntiMEVExtensionEnablingHeight[common.Hash](c.config.antiMEVEnablingHeight),
	}
	if c.config.antiMEVEnablingHeight >= 0 {
		dbftCfg = append(dbftCfg,
			dbft.WithNewPreCommit[common.Hash](c.newPreCommitCb),
			dbft.WithVerifyPreCommit[common.Hash](func(preCommit dbft.ConsensusPayload[common.Hash]) error { return nil }),
			dbft.WithNewPreBlockFromContext[common.Hash](c.newPreBlockFromContextCb),
			dbft.WithVerifyPreBlock[common.Hash](c.verifyPreBlockCb),
			dbft.WithProcessPreBlock(c.processPreBlockCb))
	}
	c.dbft, err = dbft.New[common.Hash](dbftCfg...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dBFT: %w", err)
	}

	return c, nil
}

// getKeyPairCb is a dbft library setting callback.
func (c *DBFT) getKeyPairCb(keys []dbft.PublicKey) (int, dbft.PrivateKey, dbft.PublicKey) {
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
}

// currentHeightCb is a dbft library setting callback. Consensus engine doesn't have
// access to the blockchain at the moment of call to constructor. Thus, we use `lastIndex`
// field cached in the service.
func (c *DBFT) currentHeightCb() uint32 {
	return uint32(c.lastIndex)
}

// currentBlockHashCb is a dbft library setting callback. Consensus engine doesn't have
// access to the blockchain at the moment of call to constructor. Thus, we use `lastBlockHash`
// field cached in the service.
func (c *DBFT) currentBlockHashCb() common.Hash {
	return c.lastBlockHash
}

// getValidatorsCb is a dbft library setting callback.
func (c *DBFT) getValidatorsCb(txs ...dbft.Transaction[common.Hash]) []dbft.PublicKey {
	if c.lastBlockHash.Cmp(common.Hash{}) == 0 {
		// Program bug.
		panic("last block hash wasn't initialized")
	}

	var (
		pKeys []common.Address
		err   error
	)
	if txs == nil {
		// getValidatorsSorted with empty args is used by dbft to fill the list of
		// block's validators, thus should return validators from the current
		// epoch without recalculation.
		pKeys, err = c.getValidatorsSorted(&c.lastIndex, nil, nil)
	}
	// getValidatorsSorted with non-empty args is used by dbft to fill block's
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
}

// processBlockCb is a dbft library setting callback.
func (c *DBFT) processBlockCb(b dbft.Block[common.Hash]) error {
	dbftBlock := b.(*Block)
	if uint64(dbftBlock.Index()) <= c.lastIndex {
		return nil
	}
	if dbftBlock.state == nil {
		// We're toast, proposal was invalid (likely just outdated), the node doesn't have relevant
		// block state, hence can't continue block processing. In good scenario (if proposal is valid but outdated)
		// new block arrival will trigger dBFT initialization at new height, hence don't stop the node immediately.
		log.Warn("can't process block due to missing state: proposal verification failed")
		return nil
	}

	// Avoid copying and may safely change the block itself, as this part
	// of code is guaranteed to be called once by dBFT.
	var pub *tpke.PublicKey
	if c.chain.Config().IsNeoXAMEV(dbftBlock.header.Number) {
		c.lock.RLock()
		ks := c.amevKeystore
		c.lock.RUnlock()
		pub, _ = ks.GlobalPublicKey()
	}
	witness, err := c.getBlockWitness(pub, dbftBlock)
	if err != nil {
		var count int
		for _, cm := range c.dbft.Context.CommitPayloads {
			if cm != nil {
				count++
			}
		}
		if !errors.Is(err, antimev.ErrSigAggregation) || count == c.dbft.N() {
			log.Crit("bug: unexpected error during signature aggregation", "error", err)
		}
		// Not enough valid shares, waiting for more Commits to be collected.
		return fmt.Errorf("failed to construct block witness: %w", err)
	}
	dbftBlock.header.Extra = append(dbftBlock.header.Extra, witness...) // Extra version isn't changed, validators addresses and signatures are added.
	state := dbftBlock.state.Copy()
	res := types.NewBlockWithHeader(dbftBlock.header).WithBody(dbftBlock.transactions, nil).WithWithdrawals(dbftBlock.withdrawals)

	// Firstly, notify chain about new block.
	if err := c.blockQueue.PutBlock(res, dbftBlock.state, dbftBlock.receipts); err != nil {
		// The block might already be added via the regular network
		// interaction.
		if h := c.chain.GetHeaderByNumber(res.Number().Uint64()); h == nil {
			log.Warn("error on enqueue block", "error", err.Error())
		}
	}

	c.postBlock(res.Header(), state)
	return nil
}

// newBlockFromContextCb is a dbft library setting callback.
func (c *DBFT) newBlockFromContextCb(ctx *dbft.Context[common.Hash]) dbft.Block[common.Hash] {
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

		// If we're primary, then reuse sealing state got after PrepareRequest
		// construction.
		if ctx.IsPrimary() {
			res.state = c.sealingState
			res.receipts = c.sealingReceipts
		}
		return res
	}
	pre := ctx.PreBlock().(*PreBlock)

	// Short path if we're primary and there's no Envelopes in the block:
	// reuse state got after PrepareRequest construction.
	if ctx.IsPrimary() && pre.finalState == nil {
		return &Block{
			header:              c.sealingBlock.Header(),
			withdrawals:         c.sealingBlock.Withdrawals(),
			transactions:        c.sealingBlock.Transactions(),
			localSignatureBytes: nil,
			state:               c.sealingState,
			receipts:            c.sealingReceipts,
		}
	}

	if pre.finalState == nil {
		// We're toast, proposal was invalid (likely just outdated), the node doesn't have relevant
		// block state, hence can't continue consensus. In good scenario (if proposal is valid but outdated)
		// new block arrival will trigger dBFT initialization at new height, hence don't stop the node immediately.
		log.Warn("can't construct block due to missing state: proposal verification failed")
		return nil
	}

	// Manually update header's fields based on fresh state. Avoid changing
	// the original PreBlock.
	finalBlock := &PreBlock{
		header:                 pre.header,
		transactions:           pre.finalTransactions,
		withdrawals:            pre.withdrawals,
		dkgRound:               pre.dkgRound,
		enforceECDSASignatures: c.config.enforceECDSASignatures,
	}
	ethBlock := finalBlock.ToEthBlock()
	h := ethBlock.Header()
	h.GasUsed = pre.finalGASUsed
	multisig, threshold := c.getNextConsensus(h, pre.finalState)
	// treshold may be empty if some error occurs during calculation. Don't
	// check it, we always have multisig as a backup scheme.
	h.MixDigest = threshold
	h.Extra = append(h.Extra[:dbftutil.ExtraVersionLen+dbftutil.ExtraV1SignatureSchemeLen], multisig.Bytes()...)

	// Update state root, transactions root, receipts hash and bloom.
	res, err := c.FinalizeAndAssemble(c.chain, h, pre.finalState, pre.finalTransactions, nil, pre.finalReceipts, ethBlock.Withdrawals())
	if err != nil {
		log.Crit("Failed to finalize and assemble final Block",
			"err", err)
	}

	return &Block{
		header:              res.Header(),
		withdrawals:         res.Withdrawals(),
		transactions:        res.Transactions(),
		localSignatureBytes: nil,
		state:               pre.finalState,
		receipts:            pre.finalReceipts,
	}
}

// getTxCb is a dbft library setting callback.
func (c *DBFT) getTxCb(h common.Hash) dbft.Transaction[common.Hash] {
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
}

// getVerifiedCb is a dbft library setting callback.
func (c *DBFT) getVerifiedCb() []dbft.Transaction[common.Hash] {
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
}

// requestTxCb is a dbft library setting callback.
func (c *DBFT) requestTxCb(hashes ...common.Hash) {
	if len(hashes) == 0 {
		return
	}

	sorted := slices.Clone(hashes)
	slices.SortFunc(sorted, common.Hash.Cmp)
	c.txCbList.Store(sorted)

	c.requestTxs(sorted)
}

// stopTxFlowCb is a dbft library setting callback.
func (c *DBFT) stopTxFlowCb() {
	var hashes []common.Hash
	c.txCbList.Store(hashes)
}

// newPrepareRequestCb is a dbft library setting callback.
func (c *DBFT) newPrepareRequestCb(ts uint64, nonce uint64, txHashes []common.Hash) dbft.PrepareRequest[common.Hash] {
	var req = new(prepareRequest)
	if c.sealingProposal == nil {
		panic("bug: sealing proposal is not initialized")
	}

	// Recalculate block state provided by miner and update sealing proposal if context-related
	// block fields were changed.
	dbftBlock := c.newPreBlockFromContext(c.sealingProposal)
	dbftBlock.transactions = c.sealingTransactions
	ethBlock := dbftBlock.ToEthBlock()

	state, receipts, _, gasUsed, err := c.chain.ProcessState(ethBlock, nil)
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
	multisig, threshold := c.getNextConsensus(header, state)
	if c.chain.Config().IsNeoXAMEV(new(big.Int).Add(header.Number, bigOne)) {
		header.MixDigest = threshold
	} else {
		header.MixDigest = multisig
	}

	// Enforce V1 signing scheme startign from NeoXAMEV+1 height to be able to
	// use block signature fallback mechanism starting from NeoXAMEV height.
	if c.chain.Config().IsNeoXAMEV(new(big.Int).Add(header.Number, bigOne)) {
		header.Extra = append(header.Extra[:dbftutil.ExtraVersionLen+dbftutil.ExtraV1SignatureSchemeLen], multisig.Bytes()...)
	}

	// Update state root, transactions root, receipts hash and bloom.
	res, err := c.FinalizeAndAssemble(c.chain, header, state, dbftBlock.transactions, nil, receipts, ethBlock.Withdrawals())
	if err != nil {
		log.Crit("Failed to finalize and assemble proposed block",
			"err", err)
	}

	c.sealingProposal = res.Header()
	c.sealingState = state
	c.sealingBlock = res
	c.sealingReceipts = receipts

	req.SealingProposal = c.sealingProposal
	if len(c.lastBlockSealHash) == common.HashLength {
		req.ParentSealHashV0.SetBytes(c.lastBlockSealHash)
	}
	req.ParentExtra = c.lastBlockExtra
	req.TxHashes = txHashes

	return req
}

// newCommitCb is a dbft library setting callback.
func (c *DBFT) newCommitCb(sig []byte) dbft.Commit {
	res := new(commit)
	res.signature = slices.Clone(sig)
	res.version = c.getBlockExtraVersion(big.NewInt(int64(c.dbft.Context.BlockIndex)))
	return res
}

// newPrepareResponseCb is a dbft library setting callback.
func (c *DBFT) newPrepareResponseCb(prepH common.Hash) dbft.PrepareResponse[common.Hash] {
	return &prepareResponse{
		PreparationHashExt: prepH,
	}
}

// newChangeViewCb is a dbft library setting callback.
func (c *DBFT) newChangeViewCb(newView byte, reason dbft.ChangeViewReason, ts uint64) dbft.ChangeView {
	return &changeView{
		newViewNumber: newView,
		ReasonExt:     reason,
		TimestampExt:  ts,
	}
}

// newRecoveryRequestCb is a dbft library setting callback.
func (c *DBFT) newRecoveryRequestCb(ts uint64) dbft.RecoveryRequest {
	return &recoveryRequest{
		TimestampExt: ts,
	}
}

// newRecoveryMessageCb is a dbft library setting callback.
func (c *DBFT) newRecoveryMessageCb() dbft.RecoveryMessage[common.Hash] {
	r := &recoveryMessage{
		version: c.getBlockExtraVersion(big.NewInt(int64(c.dbft.Context.BlockIndex))),
	}
	return r
}

// verifyCommitCb is a dbft library setting callback.
func (c *DBFT) verifyCommitCb(p dbft.ConsensusPayload[common.Hash]) error {
	cc := p.GetCommit().(*commit)
	h := big.NewInt(int64(p.Height()))

	if expected := c.getBlockExtraVersion(h); cc.version != expected {
		return fmt.Errorf("%w: expected %d, got %d", dbftutil.ErrUnexpectedExtraVersion, expected, cc.version)
	}

	var (
		expectedLen int
		v           = cc.version
		isAMEV      = c.chain.Config().IsNeoXAMEV(h)
	)
	switch v {
	case dbftutil.ExtraV0:
		expectedLen = crypto.SignatureLength
	case dbftutil.ExtraV1:
		if c.config.enforceECDSASignatures || (!isAMEV && c.chain.Config().IsNeoXAMEV(new(big.Int).Add(h, bigOne))) {
			expectedLen = crypto.SignatureLength
		} else {
			expectedLen = tpke.SignatureShareLen
		}
	default:
		return fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, cc.version)
	}
	if len(cc.signature) != expectedLen {
		return fmt.Errorf("invalid signature length: expected %d, got %d", expectedLen, len(cc.signature))
	}

	// Update share cache for TPKE commits.
	if expectedLen == tpke.SignatureShareLen {
		_, err := cc.share()
		if err != nil {
			return fmt.Errorf("invalid commit: %w", err)
		}
	}

	return nil
}

// verifyPrepareRequestCb is a dbft library setting callback.
func (c *DBFT) verifyPrepareRequestCb(p dbft.ConsensusPayload[common.Hash]) error {
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

	// A separate check for post-NeoXAMEV block signing scheme since it depends
	// on runtime node configuration.
	extra := dbftutil.Extra(req.SealingProposal.Extra)
	if c.chain.Config().IsNeoXAMEV(req.SealingProposal.Number) && extra.Version() == dbftutil.ExtraV1 {
		var expected = dbftutil.ExtraV1ThresholdScheme
		if c.config.enforceECDSASignatures {
			expected = dbftutil.ExtraV1ECDSAScheme
		}
		if extra.SignatureScheme() != expected {
			return fmt.Errorf("%w: expected %d, got %d", dbftutil.ErrUnexpectedBlockSignatureScheme, expected, extra.SignatureScheme())
		}
	}

	if req.SealingProposal.ParentHash != c.lastBlockHash {
		if c.chain.Config().IsNeoXAMEV(new(big.Int).Sub(req.SealingProposal.Number, bigOne)) && c.lastBlockExtra.SignatureScheme() == dbftutil.ExtraV1ThresholdScheme {
			return fmt.Errorf("invalid parent hash after NeoXAMEV with threshold signing scheme: expected %s, got %s", c.lastBlockHash, req.SealingProposal.ParentHash)
		}
		// Genesis block  is hard-coded, thus its hash (as a parent hash) must always match
		// the one that prepareRequest declares as a parent hash, otherwise it's an error.
		if c.dbft.BlockIndex <= 1 {
			return fmt.Errorf("invalid parent: expected %s, got %s", c.lastBlockHash, req.SealingProposal.ParentHash)
		}
		var expected common.Hash
		expected.SetBytes(c.lastBlockSealHash)
		if req.ParentSealHashV0 != expected {
			return fmt.Errorf("parent seal hash doesn't match the last block seal hash: expected %s, got %s", expected, req.ParentSealHashV0)
		}
		// Verify proposed parent's signature.
		savedGrandparent := c.chain.GetBlockByNumber(req.SealingProposal.Number.Uint64() - 2)
		if savedGrandparent == nil {
			return errors.New("failed to verify parent: failed to retrieve grandparent from storage")
		}
		err := c.verifyExtra(req.ParentSealHashV0.Bytes(), req.ParentExtra, savedGrandparent.Header().NextConsensus(), savedGrandparent.Extra())
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
			"sealhash", req.ParentSealHashV0.String(),
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
}

// verifyPreBlockCb is a dbft library setting callback.
func (c *DBFT) verifyPreBlockCb(b dbft.PreBlock[common.Hash]) bool {
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

	localPool := c.newLocalPool(parent)
	errs := localPool.Add(dbftBlock.transactions, false, false)
	for i, err := range errs {
		if err != nil {
			log.Warn("proposed PreBlock has invalid transaction",
				"index", i,
				"hash", dbftBlock.transactions[i].Hash(),
				"error", err,
			)
			return false
		}
	}

	state, receipts, gasUsed, err := c.chain.VerifyBlock(ethBlock, true)
	if err != nil {
		log.Warn("proposed PreBlock verification failed",
			"err", err.Error())
		return false
	}

	// Cache processing result for further usage in case if there's no envelopes
	// in the block or fallback signing scheme is used.
	pre := b.(*PreBlock)
	pre.finalState = state
	pre.finalReceipts = receipts
	pre.finalGASUsed = gasUsed

	return true
}

// verifyBlockCb is a dbft library setting callback.
func (c *DBFT) verifyBlockCb(b dbft.Block[common.Hash]) bool {
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
		state, receipts, _, err := c.chain.VerifyBlock(ethBlock, true)
		if err != nil {
			log.Warn("proposed block verification failed",
				"err", err.Error())
			return false
		}

		// Verify NextConsensus based on the state got after in-block transactions processing.
		var expected common.Hash
		multisig, threshold := c.getNextConsensus(dbftBlock.header, state)
		if c.chain.Config().IsNeoXAMEV(new(big.Int).Add(dbftBlock.header.Number, bigOne)) {
			expected = threshold
		} else {
			expected = multisig
		}
		if dbftBlock.header.MixDigest != expected {
			log.Warn("Invalid NextConsensus in the proposed block",
				"expected", expected.String(),
				"actual", dbftBlock.header.MixDigest.String())
			return false
		}

		dbftBlock.state = state
		dbftBlock.receipts = receipts

		return true
	}

	log.Crit("unexpected call to VerifyBlock")

	return false
}

// broadcastCb is a dbft library setting callback.
func (c *DBFT) broadcastCb(p dbft.ConsensusPayload[common.Hash]) {
	if err := p.(*Payload).Sign(c.dbft.Priv.(*Signer)); err != nil {
		log.Warn("can't sign consensus payload", "error", err)
	}

	ep := &p.(*Payload).Message
	err := c.broadcast(ep)
	if err != nil {
		log.Warn("can't broadcast consensus message", "error", err)
	}
}

// newPreCommitCb is a dbft library setting callback.
func (c *DBFT) newPreCommitCb(data []byte) dbft.PreCommit {
	return &preCommit{dataExt: data}
}

// newPreBlockFromContextCb is a dbft library setting callback.
func (c *DBFT) newPreBlockFromContextCb(ctx *dbft.Context[common.Hash]) dbft.PreBlock[common.Hash] {
	prepareReq := ctx.PreparationPayloads[ctx.PrimaryIndex]
	if prepareReq == nil {
		panic("can't create new PreBlock from context: prepare request is nil")
	}

	return c.newPreBlockFromContext(prepareReq.GetPrepareRequest().(*prepareRequest).SealingProposal)
}

// processPreBlockCb is a dbft library setting callback.
func (c *DBFT) processPreBlockCb(b dbft.PreBlock[common.Hash]) error {
	var (
		ctx             = c.dbft.Context
		pre             = b.(*PreBlock)
		hasDecryptedTxs bool
	)
	// A short path: there's no envelopes at all, use proposed transactions as-is.
	if len(pre.envelopesData) == 0 {
		pre.finalTransactions = pre.transactions
	} else {
		var (
			encryptedKeysCurr = make([]*tpke.CipherText, 0, len(pre.envelopesData))
			encryptedKeysPrev []*tpke.CipherText
			encryptedMsgsCurr = make([][]byte, 0, len(pre.envelopesData))
			encryptedMsgsPrev [][]byte
			currSharesCnt     int
			prevSharesCnt     int
		)
		for _, d := range pre.envelopesData {
			if d.dkgRound == pre.dkgRound {
				encryptedKeysCurr = append(encryptedKeysCurr, d.encryptedKey)
				encryptedMsgsCurr = append(encryptedMsgsCurr, d.encryptedMsg)
				currSharesCnt++
			} else {
				encryptedKeysPrev = append(encryptedKeysPrev, d.encryptedKey)
				encryptedMsgsPrev = append(encryptedMsgsPrev, d.encryptedMsg)
				prevSharesCnt++
			}
		}

		var (
			sharesCurr = make(map[int][]*tpke.DecryptionShare, ctx.M())
			sharesPrev = make(map[int][]*tpke.DecryptionShare)
			blockNum   = pre.header.Number.Uint64() - 1
		)
		for _, preC := range ctx.PreCommitPayloads {
			if preC != nil && preC.ViewNumber() == ctx.ViewNumber {
				dkgIndex, err := c.getDKGIndex(int(preC.ValidatorIndex()), blockNum)
				if err != nil {
					return fmt.Errorf("get DKG index failed: ValidatorIndex %d, block height %d", int(preC.ValidatorIndex()), blockNum)
				}
				curr, prev := preC.GetPreCommit().(*preCommit).Shares()
				if len(curr) == currSharesCnt {
					// Indexes in shares map must use DKG index.
					sharesCurr[dkgIndex] = curr
				} else {
					log.Error("number of shares for the current round mismatch",
						"round", pre.dkgRound,
						"validator", preC.ValidatorIndex(),
						"validator DKG index", dkgIndex,
						"expected", currSharesCnt,
						"actual", len(curr))
				}
				if len(prev) == prevSharesCnt {
					// Indexes in shares map must use DKG index.
					sharesPrev[dkgIndex] = prev
				} else {
					log.Error("number of shares for the previous round mismatch",
						"round", pre.dkgRound-1,
						"validator", preC.ValidatorIndex(),
						"validator DKG index", dkgIndex,
						"expected", prevSharesCnt,
						"actual", len(prev))
				}
			}
		}

		c.lock.RLock()
		ks := c.amevKeystore
		c.lock.RUnlock()
		decryptedTxsBytes, err := ks.AggregateAndDecryptWithShare(encryptedKeysCurr, encryptedMsgsCurr, sharesCurr)
		if err != nil {
			// Some shares are invalid, valid shares isn't enough to decrypt, wait for more shares to be collected.
			return fmt.Errorf("failed to decrypt Encrypted transactions for the current DKG round %d, not enough valid shares: %w", pre.dkgRound, err)
		}
		var decryptedTxsBytesPrev [][]byte
		if pre.dkgRound >= crossEpochDecryptionStartRound {
			decryptedTxsBytesPrev, err = ks.AggregateAndDecryptWithReshare(encryptedKeysPrev, encryptedMsgsPrev, sharesPrev)
			if err != nil {
				// Some shares are invalid, valid shares isn't enough to decrypt, wait for more shares to be collected.
				return fmt.Errorf("failed to decrypt Encrypted transactions for the previous DKG round %d, not enough valid shares: %w", pre.dkgRound-1, err)
			}
		}

		// Merge two slices of decrypted transactions into a single slice preserving original Envelopes order.
		if len(decryptedTxsBytes)+len(decryptedTxsBytesPrev) != len(pre.envelopesData) {
			// Some shares are invalid, valid shares isn't enough to decrypt, wait for more shares to be collected.
			return fmt.Errorf("invalid number of Decrypted transactions: expected %d, actual %d", len(pre.envelopesData), len(decryptedTxsBytes)+len(decryptedTxsBytesPrev))
		}
		var prevI int
		for i, d := range pre.envelopesData {
			if d.dkgRound != pre.dkgRound {
				decryptedTxsBytes = slices.Insert(decryptedTxsBytes, i, decryptedTxsBytesPrev[prevI])
				prevI++
			}
		}

		var (
			j                  int
			txx                = make([]*types.Transaction, len(pre.transactions))
			parent             = c.chain.GetHeader(pre.header.ParentHash, pre.header.Number.Uint64()-1)
			localPool          = c.newLocalPool(parent)
			fallbackToEnvelope = func(i int, incrementJ bool, reason string) bool {
				if len(reason) != 0 {
					log.Info("Falling back to envelope",
						"envelope hash", pre.transactions[i].Hash(),
						"envelope index", i,
						"reason", reason)
				}
				errs := localPool.Add([]*types.Transaction{pre.transactions[i]}, false, false)
				if errs[0] != nil {
					log.Info("Falling back to original set of transactions",
						"envelope hash", pre.transactions[i].Hash(),
						"envelope index", i,
						"reason", fmt.Sprintf("envelope has pool conflicts: %s", errs[0]))
					txx = pre.transactions
					hasDecryptedTxs = false
					return false
				}
				txx[i] = pre.transactions[i]
				if incrementJ {
					j++
				}
				return true
			}
		)
		for i := range pre.transactions {
			var isEnvelope = j < len(pre.envelopesData) && pre.envelopesData[j].index == i
			if !isEnvelope || // pre.transactions[i] is not an envelope, use it as-is.
				decryptedTxsBytes[j] == nil { // pre.transactions[i] is Envelope, but its content failed to be decrypted, use Envelope as-is.
				var reason string
				if isEnvelope {
					reason = "envelope data decryption failed"
				}
				if fallbackToEnvelope(i, isEnvelope, reason) {
					continue
				} else {
					break
				}
			}
			log.Info("Envelope data decrypted",
				"envelope hash", pre.transactions[i].Hash(),
				"envelope index", i,
				"data", hex.EncodeToString(decryptedTxsBytes[j]))
			var decryptedTx = new(types.Transaction)
			err := decryptedTx.UnmarshalBinary(decryptedTxsBytes[j])
			if err != nil {
				if fallbackToEnvelope(i, true, fmt.Sprintf("decrypted transaction decoding failed: %s", err)) {
					continue
				} else {
					break
				}
			}
			err = c.validateDecryptedTx(parent, decryptedTx, pre.transactions[i])
			if err != nil {
				if fallbackToEnvelope(i, true, fmt.Sprintf("decrypted transaction verification failed: %s", err)) {
					continue
				} else {
					break
				}
			}
			errs := localPool.Add([]*types.Transaction{decryptedTx}, false, false)
			if errs[0] != nil {
				if fallbackToEnvelope(i, true, fmt.Sprintf("decrypted transaction has pool conflicts: %s", errs[0].Error())) {
					continue
				} else {
					break
				}
			}
			txx[i] = decryptedTx
			j++
			hasDecryptedTxs = true
		}
		pre.finalTransactions = txx
	}

	// Use cached processing results if no new transactions were included into
	// the block compared to the original proposal.
	if !hasDecryptedTxs {
		return nil
	}

	// Process state with constructed list of transactions to fill state-dependent
	// block fields.
	finalBlock := &PreBlock{
		header:                 pre.header,
		transactions:           pre.finalTransactions,
		withdrawals:            pre.withdrawals,
		dkgRound:               pre.dkgRound,
		enforceECDSASignatures: pre.enforceECDSASignatures,
	}
	ethBlock := finalBlock.ToEthBlock()
	state, receipts, _, gasUsed, err := c.chain.ProcessState(ethBlock, nil)
	if err != nil {
		// Something went wrong, fallback to the original set of transactions and cached
		// processing results.
		log.Error("failed to process PreBlock, falling back to the original proposal",
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
		pre.finalTransactions = pre.transactions
		return nil
	}

	pre.finalState, pre.finalReceipts, pre.finalGASUsed = state, receipts, gasUsed
	return nil
}

// newLocalPool returns an initialized instance of LegacyPool with default config
// except that locals are prohibited and journal is not stored.
func (c *DBFT) newLocalPool(parent *types.Header) *legacypool.LegacyPool {
	p := legacypool.New(legacypool.Config{
		Locals:                  nil,
		NoLocals:                true,
		Journal:                 "",
		Rejournal:               legacypool.DefaultConfig.Rejournal,
		PriceLimit:              legacypool.DefaultConfig.PriceLimit,
		PriceBump:               legacypool.DefaultConfig.PriceBump,
		AccountSlots:            legacypool.DefaultConfig.AccountSlots,
		GlobalSlots:             legacypool.DefaultConfig.GlobalSlots,
		AccountQueue:            legacypool.DefaultConfig.AccountQueue,
		GlobalQueue:             legacypool.DefaultConfig.GlobalQueue,
		Lifetime:                legacypool.DefaultConfig.Lifetime,
		ReannounceTimeThreshold: legacypool.DefaultConfig.ReannounceTimeThreshold,
		ReannounceRemotes:       false,
	}, c.chain)
	p.Init(legacypool.DefaultConfig.PriceLimit, parent, func(addr common.Address, reserve bool) error { return nil })

	return p
}

// ValidateDecryptedTx checks the validity of the transaction to determine whether the outer envelope transaction should be replaced.
func (c *DBFT) validateDecryptedTx(head *types.Header, decryptedTx *types.Transaction, envelope *types.Transaction) error {
	// Make sure the transaction is signed properly and has the same sender and nonce with envelope
	if decryptedTx.Nonce() != envelope.Nonce() {
		return fmt.Errorf("decryptedTx nonce mismatch: decryptedNonce %v, envelopeNonce %v", decryptedTx.Nonce(), envelope.Nonce())
	}
	// Ensure the gasprice is high enough to replace the envelope transaction
	baseFee := head.BaseFee
	if decryptedTx.EffectiveGasTipCmp(envelope, baseFee) < 0 {
		return fmt.Errorf("decryptedTx underpriced: EffectiveGasTip needed %v, EffectiveGasTip permitted %v", envelope.EffectiveGasTipValue(baseFee), decryptedTx.EffectiveGasTipValue(baseFee))
	}
	envelopeFrom, err := types.Sender(c.signerConfig, envelope)
	if err != nil {
		return fmt.Errorf("%w: failed to retrieve envelope sender: %w", txpool.ErrInvalidSender, err)
	}
	decryptedFrom, err := types.Sender(c.signerConfig, decryptedTx)
	if err != nil {
		return fmt.Errorf("%w: failed to retrieve decrypted transaction sender: %w", txpool.ErrInvalidSender, err)
	}
	if envelopeFrom != decryptedFrom {
		return fmt.Errorf("decryptedTx from mismatch: decryptedFrom %v, envelopeFrom %v", decryptedFrom, envelopeFrom)
	}
	return nil
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
	res := &PreBlock{
		header:                 h,
		dkgRound:               uint32(c.dkgSnapshot.Round - 1),
		enforceECDSASignatures: c.config.enforceECDSASignatures,
	}
	// Withdrawals are temporary empty if Shanghai is passed.
	if c.chain.Config().IsShanghai(h.Number, h.Time) {
		res.withdrawals = emptyWithdrawals
	}

	return res
}

func (c *DBFT) getBlockWitness(pub *tpke.PublicKey, block *Block) ([]byte, error) {
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

	switch v := dbftutil.Extra(block.header.Extra).Version(); v {
	case dbftutil.ExtraV0:
		return res, nil
	case dbftutil.ExtraV1:
		// Enforce multisignature-based signing scheme for NeoXAMEV-1 height or if
		// enforcing configured.
		cfg := c.chain.Config()
		if c.config.enforceECDSASignatures || (cfg.NeoXAMEVBlock != nil && block.header.Number.Cmp(new(big.Int).Sub(cfg.NeoXAMEVBlock, bigOne)) == 0) {
			return res, nil
		}

		if pub == nil {
			return nil, errors.New("global public key is not provided")
		}
		res = dbftutil.FlattenAddresses([]dbftutil.Encodable{pub})
		shares := make(map[int]*tpke.SignatureShare)
		// Take all available shares since some of them may be invalid.
		for i := 0; i < len(vals); i++ {
			if p := dctx.CommitPayloads[i]; p != nil && p.ViewNumber() == dctx.ViewNumber {
				var err error
				blockNum := block.header.Number.Uint64() - 1
				dkgIndex, err := c.getDKGIndex(i, blockNum)
				if err != nil {
					return nil, fmt.Errorf("get DKG index failed: ValidatorIndex %d, block height %d", i, blockNum)
				}
				shares[dkgIndex], err = p.GetCommit().(*commit).share()
				if err != nil {
					// It's a program error since all commits are expected to be verified by this
					// moment and hence, contain proper share.
					log.Crit("failed to get commit share",
						"from", i,
						"error", err)
				}
			}
		}
		msg := dbftRLP(block.header)
		c.lock.RLock()
		ks := c.amevKeystore
		c.lock.RUnlock()
		sig, err := ks.AggregateAndVerifySig(msg, shares)
		if err != nil {
			return nil, fmt.Errorf("failed to aggregate signature: %w", err)
		}
		res = append(res, sig.Bytes()...)

		return res, nil
	default:
		return nil, fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, v)
	}
}

// WithEthAPIBackend initializes Eth API backend and transaction API for
// proper consensus module work.
func (c *DBFT) WithEthAPIBackend(b ethapi.Backend) {
	c.backend = &b
	c.txAPI = ethapi.NewTransactionAPI(b, new(ethapi.AddrLocker))
}

// EnforceECDSASignatures enforces ECDSA multisignature block signing scheme.
// This setting will be applied starting from NeoXAMEV fork.
func (c *DBFT) EnforceECDSASignatures() {
	c.config.enforceECDSASignatures = true
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

// postBlock is a callback that updates latest accepted block data and resets
// last proposal data. It must be called every time new block arrives from chain
// or from consensus.
func (c *DBFT) postBlock(h *types.Header, state *state.StateDB) {
	num := h.Number.Uint64()
	if c.lastIndex < num {
		c.lastTimestamp = h.Time
		c.lastIndex = h.Number.Uint64()
		c.lastBlockHash = h.Hash()
		c.lastBlockSealHash, _ = honestSealHash(h) // no error expected, h is verified.
		c.lastBlockExtra = h.Extra

		// handle DKG
		if c.lastIndex >= uint64(c.config.dkgEnablingHeight) {
			c.lock.RLock()
			ks := c.amevKeystore
			c.lock.RUnlock()
			err := c.handleDKG(c.dkgSnapshot, ks, h, state, false)
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
	vals, _, err := dbftutil.Extra(header.Extra).ECDSASigners(len(c.config.StandByValidators))
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	return vals[header.Primary()], nil
}

// Signers returns the set of Ethereum consensus node addresses that committed the
// given header. Note that for the block of same height there might be different
// set of signers returned by different nodes depending on the set of block's signatures.
func (c *DBFT) Signers(header *types.Header) ([]common.Address, error) {
	_, sigs, err := dbftutil.Extra(header.Extra).ECDSASigners(len(c.config.StandByValidators))
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	var (
		signers = make([]common.Address, len(sigs))
		h       = HonestSealHashV0(header).Bytes()
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
	vals, _, err := dbftutil.Extra(header.Extra).ECDSASigners(len(c.config.StandByValidators))
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

	// Check that the extra-data contains version.
	if len(header.Extra) < dbftutil.ExtraVersionLen {
		return errMissingVanity
	}
	var (
		cfg           = chain.Config()
		isAMEV        = cfg.IsNeoXAMEV(header.Number)
		isV1Extra     = isAMEV || cfg.IsNeoXAMEV(new(big.Int).Add(header.Number, bigOne))
		expectedExtra = dbftutil.ExtraV0
		extra         = dbftutil.Extra(header.Extra)
	)
	if isV1Extra {
		expectedExtra = dbftutil.ExtraV1
	}
	if v := extra.Version(); v != expectedExtra {
		return fmt.Errorf("%w: expected %d, got %d", dbftutil.ErrUnexpectedExtraVersion, expectedExtra, v)
	}

	// Check that extra-data contains hashable part filled.
	var expectedHashableExtraLen = dbftutil.HashableExtraV0Len
	if isV1Extra {
		expectedHashableExtraLen = dbftutil.HashableExtraV1Len
	}
	if len(header.Extra) < expectedHashableExtraLen {
		return fmt.Errorf("invalid extra hashable len: expected %d, got %d", expectedHashableExtraLen, len(header.Extra))
	}

	if isSealed {
		var (
			n        = len(c.config.StandByValidators)
			m        = crypto.GetBFTHonestNodeCount(n)
			expected int
		)
		if isV1Extra {
			if !isAMEV && extra.SignatureScheme() != dbftutil.ExtraV1ECDSAScheme {
				return fmt.Errorf("%w for pre-NeoXAMEV block: expected %d, got %d", dbftutil.ErrUnexpectedBlockSignatureScheme, dbftutil.ExtraV1ECDSAScheme, extra.SignatureScheme())
			}
			switch ss := extra.SignatureScheme(); ss {
			case dbftutil.ExtraV1ECDSAScheme:
				expected = dbftutil.HashableExtraV1Len + m*crypto.SignatureLength + n*common.AddressLength
			case dbftutil.ExtraV1ThresholdScheme:
				expected = dbftutil.HashableExtraV1Len + tpke.PublicKeyLen + tpke.SignatureLen
			default:
				return fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedBlockSignatureScheme, ss)
			}
		} else {
			expected = dbftutil.HashableExtraV0Len + m*crypto.SignatureLength + n*common.AddressLength
		}
		if len(header.Extra) != expected {
			return fmt.Errorf("invalid extra len: expected %d, got %d: %v", expected, len(header.Extra), header.Extra)
		}

		// Ensure that the mix digest is not zero.
		if !isV1Extra && header.MixDigest == (common.Hash{}) {
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

	sealHash, err := honestSealHash(header)
	if err != nil {
		return fmt.Errorf("failed to calculate hash: %w", err)
	}
	err = c.verifyExtra(sealHash, header.Extra, parent.NextConsensus(), parent.Extra)
	if err != nil {
		return fmt.Errorf("invalid extra: %w", err)
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

func (c *DBFT) verifyExtra(sealHashBytes []byte, extra dbftutil.Extra, parentNextConsensus common.Hash, parentExtra dbftutil.Extra) error {
	switch v := extra.Version(); v {
	case dbftutil.ExtraV0:
		// Resolve the authorization key and check against signers.
		vals, sigs, err := extra.ECDSASigners(len(c.config.StandByValidators))
		if err != nil {
			return fmt.Errorf("failed to retrieve validators and signatures from header: %w", err)
		}
		nextConsensus := dbftutil.GetNextConsensusHash(vals)
		if parentNextConsensus != nextConsensus {
			return fmt.Errorf("invalid V0 NextConsensus retrieved from validators addresses: expected %s, got %s", parentNextConsensus, nextConsensus)
		}
		err = crypto.VerifyMultiBFT(sealHashBytes, vals, sigs)
		if err != nil {
			return fmt.Errorf("%w: %s", errUnauthorizedSigner, err)
		}
		return nil
	case dbftutil.ExtraV1:
		switch ss := extra.SignatureScheme(); ss {
		case dbftutil.ExtraV1ECDSAScheme:
			vals, sigs, err := extra.ECDSASigners(len(c.config.StandByValidators))
			if err != nil {
				return fmt.Errorf("failed to retrieve validators and signatures from header: %w", err)
			}
			nextConsensus := dbftutil.GetNextConsensusHash(vals)

			// Retrieve backup NextConsensus for multisignature scheme from parent's Extra.
			var expected common.Hash
			switch pv := parentExtra.Version(); pv {
			case dbftutil.ExtraV0:
				expected = parentNextConsensus
			case dbftutil.ExtraV1:
				var offset = dbftutil.ExtraVersionLen + dbftutil.ExtraV1SignatureSchemeLen
				expected.SetBytes(parentExtra[offset : offset+common.HashLength])
			default:
				return fmt.Errorf("%w of parent block: %d", dbftutil.ErrUnexpectedExtraVersion, pv)
			}

			if expected != nextConsensus {
				return fmt.Errorf("invalid NextConsensus retrieved from validators addresses: expected %s, got %s", expected, nextConsensus)
			}
			err = crypto.VerifyMultiBFT(sealHashBytes, vals, sigs)
			if err != nil {
				return fmt.Errorf("%w: %s", errUnauthorizedSigner, err)
			}
			return nil
		case dbftutil.ExtraV1ThresholdScheme:
			// Resolve the threshold signature and check against global public key.
			pub, sig, err := extra.ThresholdSigners()
			if err != nil {
				return fmt.Errorf("failed to retrieve validators and signatures from header: %w", err)
			}
			nextConsensus := dbftutil.GetNextConsensusHash([]dbftutil.Encodable{pub})
			if parentNextConsensus != nextConsensus {
				return fmt.Errorf("invalid NextConsensus retrieved from global public key: expected %s, got %s", parentNextConsensus, nextConsensus)
			}
			hash := new(bls12381.G2Affine)
			_, err = hash.SetBytes(sealHashBytes)
			if err != nil {
				return fmt.Errorf("seal hash is not a G2 point on BLS12-381: %w", err)
			}

			return pub.Verify(hash, sig)
		default:
			return fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedBlockSignatureScheme, ss)
		}
	default:
		return fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, v)
	}
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

	// Set proper Extra version based on the proposal height. The rest components of
	// Header's Extra (validators addresses / global TPKE pub and validators
	// signatures) are treated as changeable and are not filled in during Prepare.
	if chain.Config().IsNeoXAMEV(new(big.Int).Add(header.Number, bigOne)) {
		var sigScheme dbftutil.ExtraV1SignatureScheme
		// Enforce multisignature block signing if we're not at NeoXAMEV yet.
		if c.config.enforceECDSASignatures || !chain.Config().IsNeoXAMEV(header.Number) {
			sigScheme = dbftutil.ExtraV1ECDSAScheme
		} else {
			sigScheme = dbftutil.ExtraV1ThresholdScheme
		}
		header.Extra = []byte{byte(dbftutil.ExtraV1), byte(sigScheme)}
	} else {
		header.Extra = []byte{byte(dbftutil.ExtraV0)}
	}

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

		// Subscribe for minted blocks prior to accessing current chain header.
		// Sealing proposal awaiting may take some time during which new blocks may
		// arrive via P2P, which may lead to the fact that c.last* fields and dBFT
		// state are out-of-date comparing to the chain's state by the end of Start.
		// Early subscription allows to ensure that no blocks can be missed by eventLoop.
		c.chainHeadSub = c.chain.SubscribeChainHeadEvent(c.chainHeadEvents)

		// Start DKG task dispatcher prior to sealing proposal awaiting since new
		// block may be discovered during awaiting which may lead to DKG-related
		// transactions submission.
		go c.loopTaskList()

		// Current head of the header chain may be above the block chain, and
		// dBFT must always be based on the latest state data (i.e. blocks), thus,
		// retrieve current chain header to initialize context and wait until chain
		// will recover and process blocks up to the known and most fresh header.
		currHeader := chain.CurrentHeader()
		c.lastIndex = currHeader.Number.Uint64()
		c.lastTimestamp = currHeader.Time
		c.lastBlockHash = currHeader.Hash()
		c.lastBlockSealHash, _ = honestSealHash(currHeader) // no error is expected, currHeader is verified.
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
		c.postBlock(ltstHeader, nil)
	} else if c.lastIndex >= uint64(c.config.dkgEnablingHeight) {
		// Manually initialize DKG snapshot based on the latest block information
		// (we're sure that state of the latest block is available by this moment,
		// miner guarantees that). We can't do it earlier because blocks chain
		// may be out of sync compared to headers chain, ref. 3721f549.
		currHeader := c.chain.GetHeaderByNumber(c.lastIndex)
		c.lock.RLock()
		ks := c.amevKeystore
		c.lock.RUnlock()
		err := c.handleDKG(c.dkgSnapshot, ks, currHeader, nil, false)
		if err != nil {
			return fmt.Errorf("failed to initialize DKG snapshot at height %d: %w", currHeader.Number.Uint64(), err)
		}
	}

	if b.ParentHash().Cmp(c.lastBlockHash) != 0 {
		// In case of chain reorg it may happen that DBFT last block cache stores
		// outdated parent hash and Extra, thus, if the rest of new parent information
		// is valid, then use it to construct new sealing proposal.
		parent := c.chain.GetHeaderByHash(b.ParentHash())
		if parent == nil {
			return fmt.Errorf("can't verify sealing task: failed to get parent from chain: expected %s, got %s", c.lastBlockHash, b.ParentHash())
		}
		// Parent hash is constant starting from NeoXAMEV+1 height in case if threshold signature is used by parent.
		if c.chain.Config().IsNeoXAMEV(new(big.Int).Sub(b.Number(), bigOne)) && dbftutil.Extra(parent.Extra).SignatureScheme() == dbftutil.ExtraV1ThresholdScheme {
			return fmt.Errorf("invalid sealing task: parent hash mismatch with NeoXAMEV fork enabled and threshold signature scheme: expected %s, got %s", c.lastBlockHash, b.ParentHash())
		}

		actual, err := honestSealHash(parent)
		if err != nil {
			return fmt.Errorf("invalid sealing task: failed to calculate Parent honest seal hash: %w", err)
		}
		if bytes.Compare(c.lastBlockSealHash, actual) != 0 {
			return fmt.Errorf("invalid sealing task: invalid Parent honest seal hash: expected %s, got %s", hex.EncodeToString(c.lastBlockSealHash), hex.EncodeToString(actual))
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
	close(c.loopTaskChan)
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

	p := payloadFromMessage(cp, c.getBlockExtraVersion)
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

func (c *DBFT) getBlockExtraVersion(height *big.Int) dbftutil.ExtraVersion {
	if c.chain.Config().IsNeoXAMEV(height) || c.chain.Config().IsNeoXAMEV(new(big.Int).Add(height, bigOne)) {
		return dbftutil.ExtraV1
	}
	return dbftutil.ExtraV0
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

func payloadFromMessage(ep *dbftproto.Message, getBlockExtraVersion func(*big.Int) dbftutil.ExtraVersion) *Payload {
	return &Payload{
		Message: *ep,
		message: message{
			getBlockExtraVersion: getBlockExtraVersion,
		},
	}
}

func (c *DBFT) validatePayload(p *Payload) error {
	h := c.chain.CurrentBlock().Number.Uint64()
	validators, err := c.getValidatorsSorted(&h, nil, nil)
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
	validators, err := c.getValidatorsSorted(&h, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get validators: %w", err)
	}
	_, found := slices.BinarySearchFunc(validators, u, common.Address.Cmp)
	if !found {
		return fmt.Errorf("address is not a validator")
	}
	return nil
}

func (c *DBFT) newConsensusPayloadCb(ctx *dbft.Context[common.Hash], t dbft.MessageType, msg any) dbft.ConsensusPayload[common.Hash] {
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
		c.postBlock(h, nil)

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
	vals, err := c.getValidatorsSorted(&h, nil, nil)
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
// and the whole Extra. Note that it does not match the behaviour of encodeSigHeader.
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
		// Do not include extra to worker's hash since miner doesn't set extra version,
		// hence, finalized tasks won't be recognizable my miner.
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

func honestSealHash(header *types.Header) ([]byte, error) {
	var (
		extra    = dbftutil.Extra(header.Extra)
		sealHash []byte
	)
	switch v := extra.Version(); v {
	case dbftutil.ExtraV0:
		sealHash = HonestSealHashV0(header).Bytes()
	case dbftutil.ExtraV1:
		switch ss := extra.SignatureScheme(); ss {
		case dbftutil.ExtraV1ECDSAScheme:
			sealHash = HonestSealHashV0(header).Bytes()
		case dbftutil.ExtraV1ThresholdScheme:
			b := HonestSealHashV1(header).Bytes()
			sealHash = b[:]
		default:
			return sealHash, fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedBlockSignatureScheme, ss)
		}
	default:
		return sealHash, fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, v)
	}
	return sealHash, nil
}

// HonestSealHashV0 returns the hash of a block prior to it being sealed. It differs
// from WorkerSealHash in that all block fields except Extra's signature bytes are being
// hashed. This hash represents a Keccaak256 hash of header.
func HonestSealHashV0(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()
	encodeSigHeader(hasher, header)
	hasher.(crypto.KeccakState).Read(hash[:])
	return hash
}

// HonestSealHashV1 returns the hash of a block prior to it being sealed. It differs
// from WorkerSealHash in that all block fields except Extra's signature bytes are being
// hashed. This hash represents a G2 point on BLS12381 curve.
func HonestSealHashV1(header *types.Header) *bls12381.G2Affine {
	res, _ := bls12381.HashToG2(dbftRLP(header), tpke.Domain)
	return &res
}

// dbftRLP returns the rlp bytes which needs to be signed for the proof-of-authority
// sealing. The RLP to sign consists of the entire header apart from the 65 byte signature
// contained at the end of the extra data.
//
// Note, the method requires the extra data to be at least 1 byte, otherwise it
// panics. This is done to avoid accidentally using both forms (signature present
// or not), which could be abused to produce different hashes for the same header.
func dbftRLP(header *types.Header) []byte {
	b := new(bytes.Buffer)
	encodeSigHeader(b, header)
	return b.Bytes()
}

func encodeSigHeader(w io.Writer, header *types.Header) {
	var hashableExtraLen int
	switch v := dbftutil.Extra(header.Extra).Version(); v {
	case dbftutil.ExtraV0:
		hashableExtraLen = dbftutil.HashableExtraV0Len
	case dbftutil.ExtraV1:
		hashableExtraLen = dbftutil.HashableExtraV1Len
	default:
		panic(fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, v)) // a dangerous program bug
	}
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
		header.Extra[:hashableExtraLen], // Yes, this will panic if extra is too short
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

// getValidatorsSorted returns validators chosen in the result of the latest
// finalized voting epoch. It calls Governance contract under the hood. The call
// is based on the provided state or (if not provided) on the state of the block
// with the specified height. Validators returned from this method are always
// sorted by bytes order (even if the list returned from governance contract is
// sorted in another way). This method uses cached values in case of validators
// requested by block height.
func (c *DBFT) getValidatorsSorted(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	res, err := c.getValidators(blockNum, state, header)
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
func (c *DBFT) getValidators(blockNum *uint64, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	if c.backend == nil {
		return nil, errors.New("eth API backend is not initialized, dBFT can't function properly")
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
	var result *core.ExecutionResult
	if state != nil {
		result, err = ethapi.DoCallAtState(ctx, *c.backend, args, state, header, nil, nil, 0, 0)
	} else if blockNum != nil {
		blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(*blockNum))
		result, err = ethapi.DoCall(ctx, *c.backend, args, blockNr, nil, nil, 0, 0)
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
		_ = c.validatorsCache.Add(*blockNum, res)
	}

	return res, err
}

// getDKGIndex returns validator dkg index (original validator index +1) by validatorIndex (ordered validator index).
func (c *DBFT) getDKGIndex(validatorIndex int, blockNum uint64) (int, error) {
	originValidators, err := c.getValidators(&blockNum, nil, nil)
	if err != nil {
		return -1, err
	}
	if validatorIndex < 0 || validatorIndex >= len(originValidators) {
		return -1, fmt.Errorf("invalid validator index: validators count is %d, requested %d", len(originValidators), validatorIndex)
	}
	orderedValidators := slices.Clone(originValidators)
	slices.SortFunc(orderedValidators, common.Address.Cmp)
	addr := orderedValidators[validatorIndex]
	dkgIndex := slices.Index(originValidators, addr) + 1
	if dkgIndex == 0 {
		panic("invalid sort")
	}
	return dkgIndex, nil
}

// getGlobalPublicKey returns TPKE global public key. If state is provided, then this state
// is used to recalculate local key based on the KeyManagement contract state. If state is
// not provided, then the node's local keystore is used to retrieve global public key.
func (c *DBFT) getGlobalPublicKey(h *types.Header, s *state.StateDB) (*tpke.PublicKey, error) {
	// Replace anti-MEV keystore and DKG snapshot with a temporary copy to avoid
	// original keystore modification.
	snapshot := c.dkgSnapshot.Copy()
	c.lock.RLock()
	keystore := c.amevKeystore.Copy()
	c.lock.RUnlock()

	err := c.handleDKG(snapshot, keystore, h, s.Copy(), true)
	if err != nil {
		return nil, fmt.Errorf("failed to handle DKG at %d: %w", h.Number.Uint64(), err)
	}

	// Recalculate global public key for the next block based on this state.
	pub, err := keystore.GlobalPublicKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get public key from keystore: %w", err)
	}
	return pub, nil
}

// getNextConsensus calculates NextConsensus hash based on the provided block height
// and DB state of this block wrt NeoXAMEV fork. It does not modify the provided
// state, so the provided state may safely be reused for further block processing.
// It always returns multisignature NextConsensus and optionally threshold
// NextConsensus (if NeoXAMEV fork is enabled and no error has encountered during
// calculation).
func (c *DBFT) getNextConsensus(h *types.Header, s *state.StateDB) (common.Hash, common.Hash) {
	var multisig, threshold common.Hash

	nextVals, err := c.getValidatorsSorted(nil, s.Copy(), h)
	if err != nil {
		log.Crit("Failed to compute next block validators",
			"err", err)
	}
	multisig = dbftutil.GetNextConsensusHash(nextVals)
	if !c.chain.Config().IsNeoXAMEV(new(big.Int).Add(h.Number, bigOne)) {
		return multisig, threshold
	}
	pub, err := c.getGlobalPublicKey(h, s)
	if err != nil {
		// Do not treat this error as critical because for case of ExtraV1 fallback signing
		// scheme an error is allowed during NextConsensus calculation. Just return an empty
		// value.
		log.Error("failed to retrieve global public key to construct next consensus",
			"err", err)
	} else {
		threshold = dbftutil.GetNextConsensusHash([]dbftutil.Encodable{pub})
	}
	return multisig, threshold
}

func (c *DBFT) shouldUpdateCommitteeAt(blockNum uint64) bool {
	return blockNum%uint64(len(c.config.StandByValidators)) == 0
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
