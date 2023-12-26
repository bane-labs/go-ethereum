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
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
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
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/nspcc-dev/dbft"
	"github.com/nspcc-dev/dbft/block"
	dbftCrypto "github.com/nspcc-dev/dbft/crypto"
	"github.com/nspcc-dev/dbft/payload"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"go.uber.org/zap"
	"golang.org/x/crypto/sha3"
	"golang.org/x/exp/slices"
)

const (
	checkpointInterval = 1024 // Number of blocks after which to save the vote snapshot to the database
	inmemorySnapshots  = 128  // Number of recent vote snapshots to keep in memory
	inmemorySignatures = 4096 // Number of recent block signatures to keep in memory

	wiggleTime = 500 * time.Millisecond // Random delay (per signer) to allow concurrent signers
)

// DBFT proof-of-authority protocol constants.
var (
	epochLength = uint64(30000) // Default number of blocks after which to checkpoint and reset the pending votes

	extraVanity = 32 // Fixed number of extra-data prefix bytes reserved for signer vanity

	nonceAuthVote = hexutil.MustDecode("0xffffffffffffffff") // Magic nonce number to vote on adding a new signer
	nonceDropVote = hexutil.MustDecode("0x0000000000000000") // Magic nonce number to vote on removing a signer.

	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.

	diffInTurn = big.NewInt(2) // Block difficulty for in-turn signatures
	diffNoTurn = big.NewInt(1) // Block difficulty for out-of-turn signatures
)

const (
	extraSeal = crypto.SignatureLength // Fixed number of extra-data suffix bytes reserved for a single signer seal
	// txSubCap is the capacity of channel that receives transaction notifications from mempool.
	txSubCap = 100
	// msgsChCap is a capacity of channel that accepts consensus messages from
	// dBFT protocol.
	msgsChCap = 100
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")

	// errInvalidCheckpointBeneficiary is returned if a checkpoint/epoch transition
	// block has a beneficiary set to non-zeroes.
	errInvalidCheckpointBeneficiary = errors.New("beneficiary in checkpoint block non-zero")

	// errInvalidVote is returned if a nonce value is something else that the two
	// allowed constants of 0x00..0 or 0xff..f.
	errInvalidVote = errors.New("vote nonce not 0x00..0 or 0xff..f")

	// errInvalidCheckpointVote is returned if a checkpoint/epoch transition block
	// has a vote nonce set to non-zeroes.
	errInvalidCheckpointVote = errors.New("vote nonce in checkpoint block non-zero")

	// errMissingVanity is returned if a block's extra-data section is shorter than
	// 32 bytes, which is required to store the signer vanity.
	errMissingVanity = errors.New("extra-data 32 byte vanity prefix missing")

	// errMissingSignature is returned if a block's extra-data section doesn't seem
	// to contain a 65 byte secp256k1 signature.
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	// errExtraSigners is returned if non-checkpoint block contain signer data in
	// their extra-data fields.
	errExtraSigners = errors.New("non-checkpoint block contains extra signer list")

	// errInvalidCheckpointSigners is returned if a checkpoint block contains an
	// invalid list of signers (i.e. non divisible by 20 bytes).
	errInvalidCheckpointSigners = errors.New("invalid signer list on checkpoint block")

	// errMismatchingCheckpointSigners is returned if a checkpoint block contains a
	// list of signers different than the one the local node calculated.
	errMismatchingCheckpointSigners = errors.New("mismatching signer list on checkpoint block")

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

	// errInvalidVotingChain is returned if an authorization list is attempted to
	// be modified via out-of-range or non-contiguous headers.
	errInvalidVotingChain = errors.New("invalid voting chain")

	// errUnauthorizedSigner is returned if a header is signed by a non-authorized entity.
	errUnauthorizedSigner = errors.New("unauthorized signer")
)

// getSignersAndSigs extracts the set of validators addresses (len(cfg.StandByValidators) number of them)
// and a set of validators signatures (BFT number of them) from a signed header.
func getSignersAndSigs(cfg *params.DBFTConfig, extra []byte) ([]common.Address, [][]byte, error) {
	// Retrieve the signature from the header extra-data
	var (
		n             = len(cfg.StandByValidators)
		m             = crypto.GetBFTHonestNodeCount(n)
		addrsBytesLen = common.AddressLength * n
		sigsBytesLen  = extraSeal * m
	)
	if len(extra) < extraVanity+addrsBytesLen+sigsBytesLen {
		return nil, nil, errMissingSignature
	}

	// Recover Ethereum addresses of validators and their signatures, preserve
	// the order that was specified in the source extra, because validators are
	// sorted and NextConsensus depends on it.
	var (
		addrs = make([]common.Address, n)
		sigs  = make([][]byte, m)
	)
	for i := range addrs {
		addrOffset := extraVanity + i*common.AddressLength
		copy(addrs[i][:], extra[addrOffset:addrOffset+common.AddressLength])
	}
	for i := range sigs {
		sigOffset := len(extra) - sigsBytesLen + i*extraSeal
		sigs[i] = extra[sigOffset : sigOffset+extraSeal]
	}

	return addrs, sigs, nil
}

// DBFT is the proof-of-authority consensus engine.
type DBFT struct {
	messages chan Payload
	// Broadcast is a callback which is called to notify the dBFT service
	// about a new consensus payload to be sent.
	broadcast func(m *dbftproto.Message) error

	// various chain/mempool events and subscription management:
	chainHeadSub    event.Subscription
	chainHeadEvents chan core.ChainHeadEvent
	txSub           event.Subscription
	txEvents        chan core.NewTxsEvent

	config *params.DBFTConfig // Consensus engine configuration parameters
	epoch  uint64             // Epoch duration left for backwards compatibility.
	db     ethdb.Database     // Database to store and retrieve snapshot checkpoints

	recents    *lru.Cache[common.Hash, *Snapshot] // Snapshots for recent block to speed up reorgs
	signatures *sigLRU                            // Signatures of recent blocks to speed up mining

	proposals map[common.Address]bool // Current list of proposals we are pushing

	signer common.Address // Ethereum address of the signing key
	signFn SignerFn       // Signer function to authorize hashes with
	lock   sync.RWMutex   // Protects the signer and proposals fields

	dbft        *dbft.DBFT
	dbftStarted atomic.Bool
	blockQueue  *blockQueue

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
	chain    ChainHeaderReader
	txpool   txPool
	quit     chan struct{}
	finished chan struct{}

	// various native contract APIs that dBFT uses.
	ethAPI        *ethapi.BlockChainAPI
	governanceABI abi.ABI

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications
}

type sealingInfo struct {
	number      uint64
	sealingHash common.Hash
	parentHash  common.Hash
}

// New creates a DBFT proof-of-authority consensus engine with the initial
// signers set to the ones provided by the user.
func New(config *params.DBFTConfig, db ethdb.Database) (*DBFT, error) {
	if config.SecondsPerBlock == 0 {
		return nil, errors.New("zero-period dBFT chain is not supported")
	}
	// Set any missing consensus parameters to their defaults
	conf := *config
	// Sort validators once to reuse the sorted list in getNextConsensus.
	// Do not change configured committee.
	conf.StandByValidators = make([]common.Address, len(conf.StandByValidators))
	copy(conf.StandByValidators, config.StandByValidators)
	slices.SortFunc(conf.StandByValidators, common.Address.Cmp)
	// Allocate the snapshot caches and create the engine
	recents := lru.NewCache[common.Hash, *Snapshot](inmemorySnapshots)
	signatures := lru.NewCache[common.Hash, common.Address](inmemorySignatures)

	c := &DBFT{
		config:     &conf,
		epoch:      epochLength,
		db:         db,
		recents:    recents,
		signatures: signatures,
		proposals:  make(map[common.Address]bool),
		blockQueue: newBlockQueue(),

		messages:        make(chan Payload, msgsChCap),
		txEvents:        make(chan core.NewTxsEvent, txSubCap),
		chainHeadEvents: make(chan core.ChainHeadEvent, 2),

		quit:     make(chan struct{}),
		finished: make(chan struct{}),

		// Temporary omit difficulty checks, as currently we don't have proper mapping from
		// dBFT primary to difficulty calculations.
		fakeDiff: true,
	}

	logger, _ := zap.NewDevelopment()
	c.dbft = dbft.New(
		dbft.WithLogger(logger),
		dbft.WithSecondsPerBlock(time.Duration(conf.SecondsPerBlock)*time.Second),
		dbft.WithGetKeyPair(func(keys []dbftCrypto.PublicKey) (int, dbftCrypto.PrivateKey, dbftCrypto.PublicKey) {
			c.lock.RLock()
			signer, signFn := c.signer, c.signFn
			c.lock.RUnlock()

			// Bail out if we're unauthorized to sign a block
			for i, validator := range keys {
				if validator.(*PublicKey).Account.Cmp(signer) == 0 {
					s := &Signer{
						Signer: signer,
						SignFn: signFn,
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
		dbft.WithCurrentHeight(func() uint32 {
			return uint32(c.lastIndex)
		}),
		dbft.WithCurrentBlockHash(func() util.Uint256 {
			return util.Uint256(c.lastBlockHash)
		}),
		dbft.WithGetValidators(func(txs ...block.Transaction) []dbftCrypto.PublicKey {
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
				pKeys, err = c.getNextBlockValidators(c.lastBlockHash, c.lastIndex, false)
			}
			// getValidators with non-empty args is used by dbft to fill block's
			// NextConsensus field, but DBFT doesn't provide WithGetConsensusAddress
			// callback and fills NextConsensus by itself via WithNewBlockFromContext
			// callback. Thus, leave pKeys empty if txes != nil.
			if err != nil {
				// Program bug.
				panic(fmt.Errorf("failed to create snapshot while retrieving Validators: %w", err))
			}
			res := make([]dbftCrypto.PublicKey, len(pKeys))
			for i, s := range pKeys {
				res[i] = &PublicKey{
					Account: s,
				}
			}
			return res
		}),
		dbft.WithProcessBlock(func(b block.Block) {
			ethBlock := b.(*Block)
			if uint64(ethBlock.Index()) <= c.lastIndex {
				return
			}

			// Avoid copying and may safely change the block itself, as this part
			// of code is guaranteed to be called once thanks to condition above,
			// c.lastIndex is updated in postBlock callback every time new block
			// with higher index is accepted.
			dBFTHeader := ethBlock.header
			dBFTHeader.Extra = append(dBFTHeader.Extra, c.getBlockWitness()...) // extraVanity isn't changed, validators addresses and signatures are added.

			res := types.NewBlockWithHeader(ethBlock.header)
			// Uncles are always nil in dBFT-like consensus.
			res = res.WithBody(ethBlock.transactions, nil)

			// Firstly, notify chain about new block.
			if err := c.blockQueue.PutBlock(res); err != nil {
				// The block might already be added via the regular network
				// interaction.
				if h := c.chain.GetHeaderByNumber(res.Number().Uint64()); h == nil {
					log.Warn("error on enqueue block", "error", err.Error())
				}
			}

			// After that, update last block cached information. Do not reset sealing
			// proposal, it will be done once new block arrives to eventLoop.
			c.postBlock(res)
		}),
		dbft.WithNewBlockFromContext(func(ctx *dbft.Context) block.Block {
			prepareReq := ctx.PreparationPayloads[ctx.PrimaryIndex]
			if prepareReq == nil {
				panic("can't create new block from context: prepare request is nil")
			}
			proposal := prepareReq.GetPrepareRequest().(*prepareRequest)
			// Avoid changing PrepareRequest itself.
			h := types.CopyHeader(proposal.SealingProposal)

			// BlockIndex -> Number
			h.Number = big.NewInt(int64(ctx.BlockIndex))

			// PrimaryIndex -> Nonce
			binary.BigEndian.PutUint64(h.Nonce[:], uint64(ctx.PrimaryIndex))

			// NextConsensus -> MixHash
			nextVals, err := c.getNextBlockValidators(c.lastBlockHash, c.lastIndex, true) // always compute as it's NextConsensus.
			if err != nil {
				panic(fmt.Errorf("failed to retrieve next block validators: %w", err))
			}
			h.MixDigest = dbftutil.GetNextConsensusHash(nextVals)

			// Do not fill block's transactions. First of all, transactions are not
			// needed for block signing or block signature verification. Secondly, some
			// transactions may be missing by the moment of call to NewBlockFromContext
			// (dBFT has only the full set of their hashes). Once all transactions are
			// fetched and the commits are collected, SetTransactions callback will be
			// called by dBFT library to properly initialize block's transactions.
			return &Block{
				header: h,
			}
		}),
		dbft.WithWatchOnly(func() bool {
			return false
		}),
		dbft.WithGetTx(func(h util.Uint256) block.Transaction {
			var hash common.Hash
			hash.SetUint256(h)
			tx := c.txpool.Get(hash)
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
		dbft.WithGetVerified(func() []block.Transaction {
			var txs types.Transactions
			// Check the sealing proposal, because c.sealingTransactions may be nil
			// in case of missing pending transactions, and it's OK.
			if c.sealingProposal == nil {
				// Program bug.
				panic("missing pending sealing work")
			}
			txs = c.sealingTransactions

			res := make([]block.Transaction, len(txs))
			for i := range txs {
				res[i] = &Transaction{
					Tx: txs[i],
				}
			}
			return res
		}),
		dbft.WithRequestTx(func(h ...util.Uint256) {
		}),
		dbft.WithGetConsensusAddress(func(keys ...dbftCrypto.PublicKey) util.Uint160 {
			// NextConsensus is filled manually in NewBlockFromContext.
			return util.Uint160{}
		}),
		dbft.WithNewConsensusPayload(c.newPayload),
		dbft.WithNewPrepareRequest(func() payload.PrepareRequest {
			var req = new(prepareRequest)
			if c.sealingProposal == nil {
				panic("bug: sealing proposal is not initialized")
			}
			// Fill in only proposal and receipts, transactions will be properly
			// set from context later in SetTransactionHashes callback.
			req.SealingProposal = c.sealingProposal

			req.ParentSealHash = c.lastBlockSealHash
			req.ParentExtra = c.lastBlockExtra

			return req
		}),
		dbft.WithNewCommit(func() payload.Commit { return new(commit) }),
		dbft.WithNewPrepareResponse(func() payload.PrepareResponse { return new(prepareResponse) }),
		dbft.WithNewChangeView(func() payload.ChangeView { return new(changeView) }),
		dbft.WithNewRecoveryRequest(func() payload.RecoveryRequest { return new(recoveryRequest) }),
		dbft.WithNewRecoveryMessage(func() payload.RecoveryMessage { return new(recoveryMessage) }),
		dbft.WithVerifyPrepareResponse(func(_ payload.ConsensusPayload) error { return nil }),
		dbft.WithVerifyPrepareRequest(func(p payload.ConsensusPayload) error {
			req := p.GetPrepareRequest().(*prepareRequest)
			if req.SealingProposal == nil {
				return errors.New("failed to verify PrepareRequest: sealing proposal is nil")
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
				savedParent := c.chain.GetBlockByNumber(req.SealingProposal.Number.Uint64() - 1)
				if savedParent == nil {
					return fmt.Errorf("failed to put proposed parent to the storage: no parent found for height %d", req.SealingProposal.Number.Uint64()-1)
				}
				newHeader := savedParent.Header()
				newHeader.Extra = req.ParentExtra
				err = c.blockQueue.PutBlock(savedParent.WithSeal(newHeader))
				if err != nil {
					err = fmt.Errorf("failed to enqueue parent with updated extra for height %d (old hash %s, new hash %s): %w",
						req.SealingProposal.Number.Uint64()-1,
						savedParent.Hash(),
						req.SealingProposal.ParentHash,
						err)
					// This error is critical for further dBFT functioning.
					log.Warn(err.Error())
					return err
				}

				c.lastBlockHash = req.SealingProposal.ParentHash
				c.lastBlockExtra = req.ParentExtra
				c.dbft.PrevHash = req.SealingProposal.ParentHash.Uint256()
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
		dbft.WithBroadcast(func(p payload.ConsensusPayload) {
			if err := p.(*Payload).Sign(c.dbft.Priv.(*Signer)); err != nil {
				log.Warn("can't sign consensus payload", "error", err)
			}

			ep := &p.(*Payload).Message
			err := c.broadcast(ep)
			if err != nil {
				log.Warn("can't broadcast consensus message", "error", err)
			}
		}),
	)

	return c, nil
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

// WithBroadcast sets callback to notify the caller about new consensus message.
func (c *DBFT) WithBroadcast(f func(m *dbftproto.Message) error) {
	c.broadcast = f
}

// WithTxPool initializes transaction pool API for DBFT interactions with memory pool
// (fetching unknown transactions).
func (c *DBFT) WithTxPool(pool txPool) {
	c.txpool = pool
}

// postBlock is a callback that updates latest accepted block data and resets
// last proposal data. It must be called every time new block arrives from chain
// or from consensus.
func (c *DBFT) postBlock(b *types.Block) {
	if c.lastIndex < b.NumberU64() {
		h := b.Header()

		c.lastTimestamp = h.Time
		c.lastIndex = h.Number.Uint64()
		c.lastBlockHash = b.Hash()
		c.lastBlockSealHash = HonestSealHash(h)
		c.lastBlockExtra = h.Extra
	}
}

// Author implements consensus.Engine, returning the Ethereum address recovered
// from the signature in the header's extra-data section.
func (c *DBFT) Author(header *types.Header) (common.Address, error) {
	vals, _, err := getSignersAndSigs(c.config, header.Extra)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to retrieve validators addresses and signatures from header: %w", err)
	}
	return vals[header.Primary()], nil
}

// VerifyHeader checks whether a header conforms to the consensus rules.
func (c *DBFT) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	return c.verifyHeader(chain, header, nil)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers. The
// method returns a quit channel to abort the operations and a results channel to
// retrieve the async verifications (the order is that of the input slice).
func (c *DBFT) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := c.verifyHeader(chain, header, headers[:i])

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
func (c *DBFT) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()

	// Don't waste time checking blocks from the future
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}
	// Checkpoint blocks need to enforce zero beneficiary
	checkpoint := (number % c.epoch) == 0
	if checkpoint && header.Coinbase != (common.Address{}) {
		return errInvalidCheckpointBeneficiary
	}
	// Nonces contain Primary index, so it's not required for them to be 0x00..0
	// ([nonceAuthVote]) or 0xff..f ([nonceDropVote]), thus, skip Nonce check.
	// It's not bound to checkpoint anymore.

	// Check that the extra-data contains both the vanity and signature
	if len(header.Extra) < extraVanity {
		return errMissingVanity
	}
	m := crypto.GetBFTHonestNodeCount(len(c.config.StandByValidators))
	sigBytesLen := m * extraSeal
	if len(header.Extra) < extraVanity+sigBytesLen {
		return errMissingSignature
	}
	// Ensure that the extra-data contains a signer list on checkpoint, but none otherwise
	signersBytes := len(header.Extra) - extraVanity - sigBytesLen
	if signersBytes == 0 {
		return fmt.Errorf("missing validators addresses")
	}
	if signersBytes%common.AddressLength != 0 {
		return errInvalidCheckpointSigners
	}
	// Ensure that the mix digest is not zero.
	if header.MixDigest == (common.Hash{}) {
		return errInvalidMixDigest
	}
	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}
	// Ensure that the block's difficulty is meaningful (may not be correct at this point)
	if number > 0 {
		if header.Difficulty == nil || (header.Difficulty.Cmp(diffInTurn) != 0 && header.Difficulty.Cmp(diffNoTurn) != 0) {
			return errInvalidDifficulty
		}
	}
	// Verify that the gas limit is <= 2^63-1
	if header.GasLimit > params.MaxGasLimit {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, params.MaxGasLimit)
	}
	if chain.Config().IsShanghai(header.Number, header.Time) {
		return errors.New("dbft does not support shanghai fork")
	}
	if chain.Config().IsCancun(header.Number, header.Time) {
		return errors.New("dbft does not support cancun fork")
	}
	// All basic checks passed, verify cascading fields
	return c.verifyCascadingFields(chain, header, parents)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (c *DBFT) verifyCascadingFields(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
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
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
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
	// Retrieve the snapshot needed to verify this header and cache it
	snap, err := c.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}
	// All basic checks passed, verify the seal and return
	return c.verifySeal(snap, header, parents, parent)
}

// snapshot retrieves the authorization snapshot at a given point in time.
func (c *DBFT) snapshot(chain consensus.ChainHeaderReader, number uint64, hash common.Hash, parents []*types.Header) (*Snapshot, error) {
	// Search for a snapshot in memory or on disk for checkpoints
	var (
		headers []*types.Header
		snap    *Snapshot
	)
	for snap == nil {
		// If an in-memory snapshot was found, use that
		if s, ok := c.recents.Get(hash); ok {
			snap = s
			break
		}
		// If an on-disk checkpoint snapshot can be found, use that
		if number%checkpointInterval == 0 {
			if s, err := loadSnapshot(c.epoch, c.config, c.db, hash); err == nil {
				log.Trace("Loaded voting snapshot from disk", "number", number, "hash", hash)
				snap = s
				break
			}
		}
		// If we're at the genesis, snapshot the initial state. Alternatively if we're
		// at a checkpoint block without a parent (light client CHT), or we have piled
		// up more headers than allowed to be reorged (chain reinit from a freezer),
		// consider the checkpoint trusted and snapshot it.
		if number == 0 || (number%c.epoch == 0 && (len(headers) > params.FullImmutabilityThreshold || chain.GetHeaderByNumber(number-1) == nil)) {
			checkpoint := chain.GetHeaderByNumber(number)
			if checkpoint != nil {
				hash := checkpoint.Hash()
				sigsBytesLen := extraSeal * crypto.GetBFTHonestNodeCount(len(c.config.StandByValidators))
				signers := make([]common.Address, (len(checkpoint.Extra)-extraVanity-sigsBytesLen)/common.AddressLength)
				for i := 0; i < len(signers); i++ {
					copy(signers[i][:], checkpoint.Extra[extraVanity+i*common.AddressLength:])
				}
				snap = newSnapshot(c.epoch, c.config, number, hash, signers)
				if err := snap.store(c.db); err != nil {
					return nil, err
				}
				log.Info("Stored checkpoint snapshot to disk", "number", number, "hash", hash)
				break
			}
		}
		// No snapshot for this header, gather the header and move backward
		var header *types.Header
		if len(parents) > 0 {
			// If we have explicit parents, pick from there (enforced)
			header = parents[len(parents)-1]
			if header.Hash() != hash || header.Number.Uint64() != number {
				return nil, consensus.ErrUnknownAncestor
			}
			parents = parents[:len(parents)-1]
		} else {
			// No explicit parents (or no more left), reach out to the database
			header = chain.GetHeader(hash, number)
			if header == nil {
				return nil, fmt.Errorf("failed to retrieve parent from DB: %w", consensus.ErrUnknownAncestor)
			}
		}
		headers = append(headers, header)
		number, hash = number-1, header.ParentHash
	}
	// Previous snapshot found, apply any pending headers on top of it
	for i := 0; i < len(headers)/2; i++ {
		headers[i], headers[len(headers)-1-i] = headers[len(headers)-1-i], headers[i]
	}
	snap, err := snap.apply(headers)
	if err != nil {
		return nil, err
	}
	c.recents.Add(snap.Hash, snap)

	// If we've generated a new checkpoint snapshot, save to disk
	if snap.Number%checkpointInterval == 0 && len(headers) > 0 {
		if err = snap.store(c.db); err != nil {
			return nil, err
		}
		log.Trace("Stored voting snapshot to disk", "number", snap.Number, "hash", snap.Hash)
	}
	return snap, err
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
func (c *DBFT) verifySeal(snap *Snapshot, header *types.Header, parents []*types.Header, parent *types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	vals, err := c.verifyExtra(HonestSealHash(header), header.Extra, parent.NextConsensus())
	if err != nil {
		return fmt.Errorf("invalid Extra: %w", err)
	}
	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !c.fakeDiff {
		signer := vals[header.Primary()]
		inturn := snap.inturn(header.Number.Uint64(), signer)
		if inturn && header.Difficulty.Cmp(diffInTurn) != 0 {
			return errWrongDifficulty
		}
		if !inturn && header.Difficulty.Cmp(diffNoTurn) != 0 {
			return errWrongDifficulty
		}
	}
	return nil
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
	// If the block isn't a checkpoint, cast a random vote (good enough for now)
	header.Coinbase = common.Address{}
	header.Nonce = types.BlockNonce{}

	number := header.Number.Uint64()
	// Assemble the voting snapshot to check which votes make sense
	snap, err := c.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return fmt.Errorf("failed to create snapshot for voting calculations: %w", err)
	}
	c.lock.RLock()
	if number%c.epoch != 0 {
		// Gather all the proposals that make sense voting on
		addresses := make([]common.Address, 0, len(c.proposals))
		for address, authorize := range c.proposals {
			if snap.validVote(address, authorize) {
				addresses = append(addresses, address)
			}
		}
		// If there's pending proposals, cast a vote on them
		if len(addresses) > 0 {
			header.Coinbase = addresses[rand.Intn(len(addresses))]
			if c.proposals[header.Coinbase] {
				copy(header.Nonce[:], nonceAuthVote)
			} else {
				copy(header.Nonce[:], nonceDropVote)
			}
		}
	}

	// Copy signer protected by mutex to avoid race condition
	signer := c.signer
	c.lock.RUnlock()

	// Set the correct difficulty
	header.Difficulty = calcDifficulty(snap, signer)

	// Ensure the extra data has all its components
	if len(header.Extra) < extraVanity {
		header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, extraVanity-len(header.Extra))...)
	}
	// Fill only extraVanity. The rest components of Header's Extra (validators
	// addresses and BFT number of validators signatures) are treated as changeable
	// and are not filled in during Prepare. These data will be set after block
	// sealing in processBlock dBFT callback.
	header.Extra = header.Extra[:extraVanity]

	// Mix digest is reserved for now, set to empty
	header.MixDigest = common.Hash{}

	// Ensure the timestamp has the correct delay
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Time = uint64(time.Now().Unix())
	return nil
}

// Finalize implements consensus.Engine. There is no post-transaction
// consensus rules in dbft, do nothing here.
func (c *DBFT) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, withdrawals []*types.Withdrawal) {
	// No block rewards in PoA, so the state remains as is
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (c *DBFT) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt, withdrawals []*types.Withdrawal) (*types.Block, error) {
	if len(withdrawals) > 0 {
		return nil, errors.New("dbft does not support withdrawals")
	}
	// Finalize block
	c.Finalize(chain, header, state, txs, uncles, nil)

	// Assign the final state root to header.
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Assemble and return the final block for sealing.
	b := types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil))

	return b, nil
}

// Authorize injects a private key into the consensus engine to mint new blocks
// with.
func (c *DBFT) Authorize(signer common.Address, signFn SignerFn) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.signer = signer
	c.signFn = signFn
}

// Start initializes last block cache, fetches fresh proposal from miner, starts
// DBFT engine event loop and starts dBFT consensus process.
func (c *DBFT) Start(chain ChainHeaderWriter) {
	if c.dbftStarted.CompareAndSwap(false, true) {
		c.chain = chain
		c.blockQueue.chain = chain

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
			log.Warn("Filed to fetch latest sealing proposal",
				"index", c.lastIndex+1,
				"err", err.Error())
		}

		log.Info("Starting dBFT engine",
			"last height", c.lastIndex,
			"last timestamp", c.lastTimestamp)
		c.dbft.Start(c.lastTimestamp * NsInS)

		// Subscribe for minted blocks and transactions from mempool.
		c.txSub = c.txpool.SubscribeNewTxsEvent(c.txEvents)
		c.chainHeadSub = c.chain.SubscribeChainHeadEvent(c.chainHeadEvents)

		go c.eventLoop()
	}
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// the local signing credentials.
func (c *DBFT) Seal(chain consensus.ChainHeaderReader, b *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
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
		time.Sleep(time.Second)
	}

	// And then retrieve proposal and check it.
	b := lastProposal
	if b.NumberU64() != c.lastIndex+1 {
		return fmt.Errorf("stale sealing task: invalid Number: expected %d, got %d", c.lastIndex+1, b.NumberU64())
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
		c.dbft.Context.PrevHash = c.lastBlockHash.Uint256()
	}

	return nil
}

func (c *DBFT) eventLoop() {
events:
	for {
		oldView := c.dbft.ViewNumber
		select {
		case <-c.quit:
			c.dbft.Timer.Stop()

			c.chainHeadSub.Unsubscribe()
			c.txSub.Unsubscribe()
			break events
		case <-c.dbft.Timer.C():
			hv := c.dbft.Timer.HV()
			log.Debug("timer fired",
				"height", hv.Height,
				"view", uint(hv.View))
			c.dbft.OnTimeout(hv)
		case msg := <-c.messages:
			fields := []any{
				"from", msg.message.ValidatorIndex,
				"type", msg.Type().String(),
			}

			if msg.Type() == payload.RecoveryMessageType {
				rec := msg.GetRecoveryMessage().(*recoveryMessage)
				if rec.preparationHash == nil {
					req := rec.GetPrepareRequest(&msg, c.dbft.Validators, uint16(c.dbft.PrimaryIndex))
					if req != nil {
						h := req.Hash()
						rec.preparationHash = &h
					}
				}

				fields = append(fields,
					"#preparation", len(rec.preparationPayloads),
					"#commit", len(rec.commitPayloads),
					"#changeview", len(rec.changeViewPayloads),
					"#request", rec.prepareRequest != nil,
					"#hash", rec.preparationHash != nil)
			}
			log.Debug("received message", fields...)
			c.dbft.OnReceive(&msg)
		case txs := <-c.txEvents:
			for _, tx := range txs.Txs {
				c.dbft.OnTransaction(&Transaction{Tx: tx})
			}
		case b := <-c.chainHeadEvents:
			c.handleChainBlock(b.Block)
		case <-c.txSub.Err():
			// System has stopped.
			break events
		case <-c.chainHeadSub.Err():
			// System has stopped.
			break events
		}
		newView := c.dbft.ViewNumber
		// If ChangeView has happened, we always need to wait for the new proposal
		// from miner.
		if newView > oldView {
			log.Info("Change view detected, waiting for new sealing task to be submitted by miner", "old view", oldView, "new view", newView)
			err := c.waitForNewSealingProposal(uint64(c.dbft.Context.BlockIndex), true)
			if err != nil {
				log.Warn("Filed to fetch latest sealing proposal",
					"index", c.dbft.Context.BlockIndex,
					"err", err.Error())
			}
			log.Info("Start dBFT process for updated sealing work",
				"index", c.dbft.Context.BlockIndex,
				"view", newView,
			)
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
			c.handleChainBlock(latestBlock.Block)
		}
	}
drainLoop:
	for {
		select {
		case <-c.messages:
		case <-c.txEvents:
		case <-c.chainHeadEvents:
		default:
			break drainLoop
		}
	}
	close(c.messages)
	close(c.txEvents)
	close(c.chainHeadEvents)
	close(c.finished)
}

// OnPayload handles Payload receive.
func (c *DBFT) OnPayload(cp *dbftproto.Message) error {
	if c.dbft == nil || !c.dbftStarted.Load() {
		log.Debug("skip dBFT payload handling: dbft is inactive or not started yet", "hash", cp.Hash())
		return nil
	}

	p := payloadFromMessage(cp)
	// decode payload data into message
	if err := p.decodeData(); err != nil {
		log.Info("can't decode payload data", "hash", cp.Hash(), "error", err)
		return nil
	}

	if !c.validatePayload(p) {
		log.Info("can't validate payload", "hash", cp.Hash())
		return nil
	}

	c.messages <- *p
	return nil
}

func payloadFromMessage(ep *dbftproto.Message) *Payload {
	return &Payload{
		Message: *ep,
		message: message{},
	}
}

func (c *DBFT) validatePayload(p *Payload) bool {
	h := c.chain.CurrentHeader()
	validators, err := c.getNextBlockValidators(h.Hash(), h.Number.Uint64(), false)
	if err != nil {
		return false
	}
	if int(p.message.ValidatorIndex) >= len(validators) {
		return false
	}

	val := validators[p.message.ValidatorIndex]
	return p.Sender == val
}

func (c *DBFT) newPayload(ctx *dbft.Context, t payload.MessageType, msg any) payload.ConsensusPayload {
	cp := &Payload{}
	cp.SetHeight(ctx.BlockIndex)
	cp.SetValidatorIndex(uint16(ctx.MyIndex))
	cp.SetViewNumber(ctx.ViewNumber)
	cp.SetType(t)
	cp.SetPayload(msg)

	cp.Message.ValidBlockStart = 0
	cp.Message.ValidBlockEnd = uint64(ctx.BlockIndex)
	cp.Message.Sender = ctx.Validators[ctx.MyIndex].(*PublicKey).Account

	return cp
}

func (c *DBFT) handleChainBlock(b *types.Block) {
	// We can get our own block here, so check for index.
	if uint32(b.Number().Uint64()) >= c.dbft.BlockIndex {
		log.Info("New block in the chain",
			"dbft index", c.dbft.BlockIndex,
			"chain index", c.chain.CurrentHeader().Number.Uint64(),
			"hash", b.Hash().String(),
			"parent hash", b.ParentHash().String())
		c.postBlock(b)

		err := c.waitForNewSealingProposal(c.lastIndex+1, false)
		if err != nil {
			log.Warn("Filed to fetch latest sealing proposal",
				"index", c.lastIndex+1,
				"err", err.Error())
		}
		c.dbft.InitializeConsensus(0, c.lastTimestamp*NsInS)
	}
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
// that a new block should have:
// * DIFF_NOTURN(2) if BLOCK_NUMBER % SIGNER_COUNT != SIGNER_INDEX
// * DIFF_INTURN(1) if BLOCK_NUMBER % SIGNER_COUNT == SIGNER_INDEX
func (c *DBFT) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	snap, err := c.snapshot(chain, parent.Number.Uint64(), parent.Hash(), nil)
	if err != nil {
		return nil
	}
	c.lock.RLock()
	signer := c.signer
	c.lock.RUnlock()
	return calcDifficulty(snap, signer)
}

func calcDifficulty(snap *Snapshot, signer common.Address) *big.Int {
	if snap.inturn(snap.Number+1, signer) {
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
// dBFT during block sealing: every header field except MixDigest, Nonce and last
// [crypto.SignatureLength] bytes of Extra.
func encodeUnchangeableHeader(w io.Writer, header *types.Header) {
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
		// Do not include validators addresses into hashable part.
		header.Extra[:extraVanity], // Yes, this will panic if extra is too short.
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		panic("unexpected withdrawal hash value in dbft")
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
		header.Extra[:extraVanity], // Yes, this will panic if extra is too short
		header.MixDigest,
		header.Nonce,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		panic("unexpected withdrawal hash value in dbft")
	}
	if err := rlp.Encode(w, enc); err != nil {
		panic("can't encode: " + err.Error())
	}
}

// Close implements consensus.Engine.
func (c *DBFT) Close() error {
	close(c.quit)
	<-c.finished
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (c *DBFT) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{{
		Namespace: "dbft",
		Service:   &API{chain: chain, clique: c},
	}}
}

// getNextBlockValidators returns next block validators that should be set as
// a NextConsensus address for the next block accepted after block with blockHash
// hash and blockNum height (if compute is true). It also returns validators of
// the currently processing blocks to properly initialize dBFT context's Validators
// field (if compute is false). Validators returned from this method are always expected
// to be sorted by bytes order (even if returned from governance contract).
func (c *DBFT) getNextBlockValidators(blockHash common.Hash, blockNum uint64, compute bool) ([]common.Address, error) {
	// Currently we don't have governance contract, thus, always return standby set.
	if true {
		return c.config.StandByValidators, nil
	}

	if c.ethAPI == nil {
		return nil, errors.New("eth blockchain API is not initialized, dBFT can't function properly")
	}

	// Once we have governance contract, we don't need StandByValidators in the dBFT's
	// config, governance contract will handle it internally.
	blockNr := rpc.BlockNumberOrHashWithHash(blockHash, false)

	// Different values depending on dBFT epoch.
	method := "getNextBlockValidators" // current epoch validators
	if compute {
		method = "computeNextBlockValidators" // current epoch validators for the middle of dBFT epoch and next epoch validators for the last block in epoch
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel when we are finished consuming integers
	defer cancel()
	data, err := c.governanceABI.Pack(method)
	if err != nil {
		log.Error("Unable to pack tx to retrieve next block validators", "error", err)
		return nil, err
	}
	// do smart contract call
	msgData := (hexutil.Bytes)(data)
	toAddress := common.HexToAddress(systemcontracts.GovernanceContract)
	gas := (hexutil.Uint64)(uint64(math.MaxUint64 / 2))
	result, err := c.ethAPI.Call(ctx, ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &toAddress,
		Data: &msgData,
	}, blockNr, nil, nil)
	if err != nil {
		return nil, err
	}

	var valSet []common.Address
	err = c.governanceABI.UnpackIntoInterface(&valSet, method, result)
	return valSet, err
}

func (c *DBFT) shouldUpdateCommitteeAt(blockNum uint64) bool {
	return blockNum%uint64(len(c.config.StandByValidators)) == 0
}
