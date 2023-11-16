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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/nspcc-dev/dbft"
	"github.com/nspcc-dev/dbft/block"
	dbftCrypto "github.com/nspcc-dev/dbft/crypto"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"go.uber.org/zap"
	"golang.org/x/crypto/sha3"
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

	extraVanity = 32                     // Fixed number of extra-data prefix bytes reserved for signer vanity
	extraSeal   = crypto.SignatureLength // Fixed number of extra-data suffix bytes reserved for signer seal

	nonceAuthVote = hexutil.MustDecode("0xffffffffffffffff") // Magic nonce number to vote on adding a new signer
	nonceDropVote = hexutil.MustDecode("0x0000000000000000") // Magic nonce number to vote on removing a signer.

	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.

	diffInTurn = big.NewInt(2) // Block difficulty for in-turn signatures
	diffNoTurn = big.NewInt(1) // Block difficulty for out-of-turn signatures
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

	// errRecentlySigned is returned if a header is signed by an authorized entity
	// that already signed a header recently, thus is temporarily not allowed to.
	errRecentlySigned = errors.New("recently signed")
)

// ecrecover extracts the Ethereum account address from a signed header.
func ecrecover(header *types.Header, sigcache *sigLRU) (common.Address, error) {
	// If the signature's already cached, return that
	hash := header.Hash()
	if address, known := sigcache.Get(hash); known {
		return address, nil
	}
	// Retrieve the signature from the header extra-data
	if len(header.Extra) < extraSeal {
		return common.Address{}, errMissingSignature
	}
	signature := header.Extra[len(header.Extra)-extraSeal:]

	// Recover the public key and the Ethereum address
	pubkey, err := crypto.Ecrecover(HonestSealHash(header).Bytes(), signature)
	if err != nil {
		return common.Address{}, err
	}
	var signer common.Address
	copy(signer[:], crypto.Keccak256(pubkey[1:])[12:])

	sigcache.Add(hash, signer)
	return signer, nil
}

// DBFT is the proof-of-authority consensus engine.
type DBFT struct {
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
	blockQueue  chan *Block

	// lastTimestamp, lastIndex and lastBlockHash are updated on every new header
	// received from dBFT or from chain. These fields have exactly those type
	// that Eth offers, thus, they need to be converted before feeding to dBFT.
	lastTimestamp uint64 // in seconds, like Eth requires.
	lastIndex     uint64
	lastBlockHash common.Hash

	lastProposalLock sync.RWMutex
	lastProposal     *types.Block
	lastReceipts     []*types.Receipt

	sealingLock     sync.RWMutex
	isSealing       bool
	sealingProposal *types.Block
	sealingReceipts []*types.Receipt

	// chain instance needed for proper dBFT callbacks functioning.
	chain    consensus.ChainHeaderReader
	quit     chan struct{}
	finished chan struct{}

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications
}

// New creates a DBFT proof-of-authority consensus engine with the initial
// signers set to the ones provided by the user.
func New(config *params.DBFTConfig, db ethdb.Database) *DBFT {
	// Set any missing consensus parameters to their defaults
	conf := *config
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
		quit:       make(chan struct{}),
		finished:   make(chan struct{}),
	}

	logger, _ := zap.NewDevelopment()
	c.blockQueue = make(chan *Block)
	c.dbft = dbft.New(
		dbft.WithLogger(logger),
		dbft.WithSecondsPerBlock(time.Duration(conf.TimePerBlock)*time.Second),
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

			snap, err := c.snapshot(c.chain, c.lastIndex, c.lastBlockHash, nil)
			if err != nil {
				// Program bug.
				panic(fmt.Errorf("failed to create snapshot while retrieving Validators: %w", err))
			}
			res := make([]dbftCrypto.PublicKey, 0, len(snap.Signers))
			// Once signers are properly fetched from the Neo contract, we need to
			// sort them so that dBFT can rely on the validator's index. Currently,
			// they are sorted in ascending order.
			for _, s := range snap.signers() {
				res = append(res, &PublicKey{
					Account: s,
				})
			}
			return res
		}),
		dbft.WithProcessBlock(func(b block.Block) {
			ethBlock := b.(*Block)
			c.blockQueue <- ethBlock
			// Do not call postBlock here to avoid race between the next pending sealing request that
			// wants to update the same values as postBlock. Call postBlock in Seal instead.
		}),
		dbft.WithNewBlockFromContext(func(ctx *dbft.Context) block.Block {
			var (
				proposal *types.Block
				receipts []*types.Receipt
			)
			c.sealingLock.RLock()
			if c.sealingProposal == nil {
				// Program bug.
				panic("can't create new block from context: sealing proposal is not initialized")
			}
			proposal = c.sealingProposal
			receipts = c.sealingReceipts
			c.sealingLock.RUnlock()

			h := types.CopyHeader(proposal.Header())

			// BlockIndex -> Number
			h.Number = big.NewInt(int64(ctx.BlockIndex))

			// PrimaryIndex -> Nonce
			binary.BigEndian.PutUint64(h.Nonce[:], uint64(ctx.PrimaryIndex))

			// NextConsensus -> MixHash
			// This should be NextBlockValidators address, but for now we just
			// put single validator account.
			c.lock.RLock()
			signer, _ := c.signer, c.signFn
			c.lock.RUnlock()
			h.MixDigest.SetBytes(signer.Bytes())

			// PrevHash -> ParentHash
			h.ParentHash.SetBytes(ctx.PrevHash.BytesBE())

			txs := make([]*types.Transaction, len(ctx.Transactions))
			for i, txH := range ctx.TransactionHashes {
				txs[i] = ctx.Transactions[txH].(*Transaction).Tx
			}

			return &Block{
				Block: types.NewBlock(h, txs, nil, receipts, trie.NewStackTrie(nil)),
			}
		}),
		dbft.WithWatchOnly(func() bool {
			return false
		}),
		dbft.WithGetVerified(func() []block.Transaction {
			var txs types.Transactions
			c.sealingLock.RLock()
			if c.sealingProposal == nil {
				// Program bug.
				panic("missing pending sealing work")
			}
			txs = c.sealingProposal.Transactions()
			c.sealingLock.RUnlock()

			res := make([]block.Transaction, len(txs))
			for i := range txs {
				res[i] = &Transaction{
					Tx: txs[i],
				}
			}
			return res
		}),
		dbft.WithGetConsensusAddress(func(keys ...dbftCrypto.PublicKey) util.Uint160 {
			// NextConsensus is filled manually in NewBlockFromContext.
			return util.Uint160{}
		}),
	)

	return c
}

// postBlock is a callback that updates latest accepted block data and resets
// last proposal data. It must be called every time new block arrives from chain
// or from consensus.
func (c *DBFT) postBlock(b *types.Block) {
	if c.lastIndex < b.NumberU64() {
		c.lastTimestamp = b.Time()
		c.lastIndex = b.Number().Uint64()
		c.lastBlockHash = b.Hash()

		c.sealingLock.Lock()
		c.sealingProposal = nil
		c.sealingReceipts = nil
		c.sealingLock.Unlock()
	}
}

// Author implements consensus.Engine, returning the Ethereum address recovered
// from the signature in the header's extra-data section.
func (c *DBFT) Author(header *types.Header) (common.Address, error) {
	return ecrecover(header, c.signatures)
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
	if len(header.Extra) < extraVanity+extraSeal {
		return errMissingSignature
	}
	// Ensure that the extra-data contains a signer list on checkpoint, but none otherwise
	signersBytes := len(header.Extra) - extraVanity - extraSeal
	if !checkpoint && signersBytes != 0 {
		return errExtraSigners
	}
	if checkpoint && signersBytes%common.AddressLength != 0 {
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
		return errors.New("clique does not support shanghai fork")
	}
	if chain.Config().IsCancun(header.Number, header.Time) {
		return errors.New("clique does not support cancun fork")
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
	// If the block is a checkpoint block, verify the signer list
	if number%c.epoch == 0 {
		signers := make([]byte, len(snap.Signers)*common.AddressLength)
		for i, signer := range snap.signers() {
			copy(signers[i*common.AddressLength:], signer[:])
		}
		extraSuffix := len(header.Extra) - extraSeal
		if !bytes.Equal(header.Extra[extraVanity:extraSuffix], signers) {
			return errMismatchingCheckpointSigners
		}
	}
	// All basic checks passed, verify the seal and return
	return c.verifySeal(snap, header, parents)
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
			if s, err := loadSnapshot(c.epoch, c.signatures, c.db, hash); err == nil {
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

				signers := make([]common.Address, (len(checkpoint.Extra)-extraVanity-extraSeal)/common.AddressLength)
				for i := 0; i < len(signers); i++ {
					copy(signers[i][:], checkpoint.Extra[extraVanity+i*common.AddressLength:])
				}
				snap = newSnapshot(c.epoch, c.signatures, number, hash, signers)
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
func (c *DBFT) verifySeal(snap *Snapshot, header *types.Header, parents []*types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// Resolve the authorization key and check against signers
	signer, err := ecrecover(header, c.signatures)
	if err != nil {
		return err
	}
	if _, ok := snap.Signers[signer]; !ok {
		return errUnauthorizedSigner
	}
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only fail if the current block doesn't shift it out
			if limit := uint64(len(snap.Signers)/2 + 1); seen > number-limit {
				return errRecentlySigned
			}
		}
	}
	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !c.fakeDiff {
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
	header.Extra = header.Extra[:extraVanity]

	if number%c.epoch == 0 {
		for _, signer := range snap.signers() {
			header.Extra = append(header.Extra, signer[:]...)
		}
	}
	header.Extra = append(header.Extra, make([]byte, extraSeal)...)

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
// consensus rules in clique, do nothing here.
func (c *DBFT) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, withdrawals []*types.Withdrawal) {
	// No block rewards in PoA, so the state remains as is
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (c *DBFT) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt, withdrawals []*types.Withdrawal) (*types.Block, error) {
	if len(withdrawals) > 0 {
		return nil, errors.New("clique does not support withdrawals")
	}
	// Finalize block
	c.Finalize(chain, header, state, txs, uncles, nil)

	// Assign the final state root to header.
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Assemble and return the final block for sealing.
	b := types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil))

	// Save proposal so that the most fresh data will be available for dBFT once
	// the new block should be created.
	c.lastProposalLock.Lock()
	c.lastProposal = b
	c.lastReceipts = receipts
	c.lastProposalLock.Unlock()

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

func (c *DBFT) Initialize(chain consensus.ChainHeaderReader) {
	c.chain = chain

	currHeader := chain.CurrentHeader()
	c.lastIndex = currHeader.Number.Uint64()
	c.lastTimestamp = currHeader.Time
	c.lastBlockHash = currHeader.Hash()

	// Do not start consensus immediately, we don't yet have sealing work.
	// Start it once we have new sealing work in Seal.
	c.dbft.InitializeConsensus(0, c.lastTimestamp*NsInS)

	go c.eventLoop()
}

func (c *DBFT) eventLoop() {
events:
	for {
		select {
		case <-c.quit:
			c.dbft.Timer.Stop()
			break events
		}
	}
drainLoop:
	for {
		select {
		default:
			break drainLoop
		}
	}
	close(c.finished)
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// the local signing credentials.
func (c *DBFT) Seal(chain consensus.ChainHeaderReader, b *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	c.sealingLock.Lock()
	if c.isSealing {
		c.sealingLock.Unlock()
		return errors.New("previous sealing is not yet finished")
	}
	if b.NumberU64() != c.lastIndex+1 {
		c.sealingLock.Unlock()
		return fmt.Errorf("stale sealing task: invalid Number: expected %d, got %d", c.lastIndex+1, b.NumberU64())
	}
	if b.ParentHash().Cmp(c.lastBlockHash) != 0 {
		c.sealingLock.Unlock()
		return fmt.Errorf("stale sealing task: invalid ParentHash: expected %s, got %s", c.lastBlockHash, b.ParentHash())
	}

	c.lastProposalLock.RLock()
	if c.lastProposal == nil {
		c.lastProposalLock.RUnlock()
		c.sealingLock.Unlock()
		return errors.New("no initialized pending sealing task")
	}
	c.sealingProposal = c.lastProposal
	c.sealingReceipts = c.lastReceipts
	c.lastProposalLock.RUnlock()

	c.isSealing = true
	c.sealingLock.Unlock()

	header := b.Header()

	// Sealing the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// For 0-period chains, refuse to seal empty blocks (no reward but would spin sealing)
	if c.config.TimePerBlock == 0 && len(b.Transactions()) == 0 {
		return errors.New("sealing paused while waiting for transactions")
	}
	// Don't hold the signer fields for the entire sealing procedure
	c.lock.RLock()
	signer, _ := c.signer, c.signFn
	c.lock.RUnlock()

	// Bail out if we're unauthorized to sign a block
	snap, err := c.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}

	// This check is duplicated in dBFT.WithGetKeyPair, but here we still need to
	// keep it in order not to run the consensus if we're not the consensus node.
	if _, authorized := snap.Signers[signer]; !authorized {
		return errUnauthorizedSigner
	}

	// If we're amongst the recent signers, wait for the next block
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only wait if the current block doesn't shift it out
			if limit := uint64(len(snap.Signers)/2 + 1); number < limit || seen > number-limit {
				return errors.New("signed recently, must wait for others")
			}
		}
	}

	// Start dBFT once and afterward reinitialize it every time new block should be accepted.
	if c.dbftStarted.CompareAndSwap(false, true) {
		go c.dbft.Start(c.lastTimestamp * NsInS)
	} else {
		c.dbft.InitializeConsensus(0, c.lastTimestamp*NsInS)
		go func() {
			<-c.dbft.Timer.C()
			hv := c.dbft.Timer.HV()
			log.Info("dBFT timer fired",
				"height", hv.Height,
				"view", uint(hv.View))
			c.dbft.OnTimeout(hv)
		}()
	}

	var (
		sighash   []byte
		dBFTBlock *types.Block
	)
blocksLoop:
	for {
		select {
		case n3B := <-c.blockQueue:
			if uint64(n3B.Index()) <= c.lastIndex {
				continue blocksLoop
			}
			sighash = n3B.Signature()
			dBFTBlock = n3B.Block // this block is completely different from the one that FinalizeAndAssemble proposed.
			break blocksLoop
		case <-time.After(15 * time.Second): // for testing purposes only, remove in prod.
			return errors.New("failed to collect block with dBFT")
		}
	}
	dBFTHeader := dBFTBlock.Header()
	copy(dBFTHeader.Extra[len(dBFTHeader.Extra)-extraSeal:], sighash)

	// Completely replace proposed block with dBFT's one and relay it to the network.
	res := dBFTBlock.WithSeal(dBFTHeader)
	c.postBlock(res)

	// Seal interrupt is not possible with dBFT, thus, ignore stop channel and don't
	// wait for any delay, because dBFT provides proper block timing.
	select {
	case results <- res:
	default:
		log.Warn("Sealing result is not read by miner", "sealhash", WorkerSealHash(dBFTHeader))
	}

	c.sealingLock.Lock()
	c.isSealing = false
	c.sealingLock.Unlock()

	return nil
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
		header.Extra[:len(header.Extra)-crypto.SignatureLength], // Yes, this will panic if extra is too short
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		panic("unexpected withdrawal hash value in clique")
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
		header.Extra[:len(header.Extra)-crypto.SignatureLength], // Yes, this will panic if extra is too short
		header.MixDigest,
		header.Nonce,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if header.WithdrawalsHash != nil {
		panic("unexpected withdrawal hash value in clique")
	}
	if err := rlp.Encode(w, enc); err != nil {
		panic("can't encode: " + err.Error())
	}
}

// Close implements consensus.Engine. It's a noop for clique as there are no background threads.
func (c *DBFT) Close() error {
	close(c.quit)
	<-c.finished
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (c *DBFT) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{{
		Namespace: "clique",
		Service:   &API{chain: chain, clique: c},
	}}
}
