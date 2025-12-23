package core

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip4844"
	"github.com/ethereum/go-ethereum/core/filesystem"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
)

var (
	errBlobTxNotFound       = fmt.Errorf("blob tx not found in blob pool")
	errUnknownBlock         = fmt.Errorf("unknown block")
	errBlobNumberMismatch   = fmt.Errorf("blob number mismatch")
	errNoBlobsInBlock       = fmt.Errorf("no blobs in block")
	errSidecarsBeforeCancun = fmt.Errorf("sidecars present in block body before cancun")
	errTooManyBlobsInBlock  = fmt.Errorf("too many blobs in block")
)

type BlobPool interface {
	// Get returns a transaction if it is contained in the pool, or nil otherwise.
	Get(hash common.Hash) *types.Transaction
}

// FileSystem manages blob sidecars associated with blocks.
type FileSystem struct {
	bc          *BlockChain
	blobPool    BlobPool
	blobStorage *filesystem.BlobStorage

	blobFeed event.Feed
}

func NewFileSystem(bc *BlockChain, blobPool BlobPool, blobStorage *filesystem.BlobStorage) (*FileSystem, error) {
	fs := &FileSystem{
		bc:          bc,
		blobPool:    blobPool,
		blobStorage: blobStorage,
	}
	return fs, nil
}

// CommitSealBlockHash commits the sealed block hash associated with committed blobs.
func (fs *FileSystem) CommitSealBlockHash(block *types.Block) error {
	log.Trace("Commit seal blobs...", "number", block.Number(), "hash", block.Hash(), "txs", len(block.Transactions()))

	// Rebuild blob sidecars from blobpool
	var blobs types.BlobSidecars
	for _, tx := range block.Transactions() {
		if tx.Type() != types.BlobTxType {
			continue
		}
		originTx := fs.blobPool.Get(tx.Hash())
		if originTx == nil {
			log.Error("blob tx %s not found in blob pool", tx.Hash())
			return errBlobTxNotFound
		}
		blobs = append(blobs, originTx.BlobTxSidecar())
	}

	if !fs.bc.Config().IsCancun(block.Number(), block.Time()) {
		if blobs != nil {
			return errSidecarsBeforeCancun
		}
	}
	if len(blobs) > eip4844.MaxBlobsPerBlock(fs.bc.Config(), block.Time()) {
		return errTooManyBlobsInBlock
	}
	if len(blobs) == 0 {
		// No blobs in this block, nothing to store.
		return nil
	}

	// Store blobs to local storage.
	fs.saveBlobs(block.Header(), blobs)
	fs.blobFeed.Send(BlobEvent{
		BlockHash: block.Hash(),
		Sidecars:  blobs,
	})
	log.Trace("Commit seal blobs success", "number", block.Number(), "hash", block.Hash())
	return nil
}

// GetSidecarsByRoot retrieves the blobs associated with a block hash.
// Returns nil if not found, please try to fetch from blob protocol if so.
func (fs *FileSystem) GetSidecarsByRoot(hash common.Hash) types.BlobSidecars {
	block := fs.bc.GetBlockByHash(hash)
	if block == nil {
		log.Warn("Get sidecars by unknown block root", "hash", hash)
		return nil
	}
	txs := block.Transactions()
	blobTxCount := 0
	for _, tx := range txs {
		if tx.Type() == types.BlobTxType {
			blobTxCount++
		}
	}

	// Retrieve blobs from persistent storage with header hash.
	blobSidecars := make(types.BlobSidecars, blobTxCount)
	for i := range blobTxCount {
		if vro, err := fs.blobStorage.Get(hash, uint64(i)); err == nil {
			blobSidecars[i] = &vro.BlobSidecar.BlobTxSidecar
		} else {
			return nil
		}
	}
	return blobSidecars
}

// GetBlobTxSidecar retrieves the blob sidecar for a given block hash and index.
func (fs *FileSystem) GetBlobTxSidecar(hash common.Hash, index uint64) *types.BlobTxSidecar {
	block := fs.bc.GetBlockByHash(hash)
	if block == nil {
		return nil
	}
	txs := block.Transactions()
	blobTxCount := 0
	for _, tx := range txs {
		if tx.Type() == types.BlobTxType {
			blobTxCount++
		}
	}
	if index >= uint64(blobTxCount) {
		return nil
	}

	// Retrieve blobs from persistent storage with header hash.
	vro, err := fs.blobStorage.Get(hash, uint64(index))
	if err != nil {
		return nil
	}
	return &vro.BlobSidecar.BlobTxSidecar

}

// InsertBlobs inserts blobs for a given block hash.
func (fs *FileSystem) InsertBlobs(hash common.Hash, blobs types.BlobSidecars) error {
	block := fs.bc.GetBlockByHash(hash)
	if block == nil {
		return errUnknownBlock
	}
	// Check if we should retain the blobs based on retention policy
	if !fs.shouldRetain(block.Number()) {
		return nil
	}
	if err := fs.checkBlobsAvailable(block, blobs); err != nil {
		return err
	}

	// Store blobs to local storage.
	fs.saveBlobs(block.Header(), blobs)
	return nil
}

// SubscribeBlobsEvent subscribes to blob events.
func (fs *FileSystem) SubscribeBlobsEvent(ch chan<- BlobEvent) event.Subscription {
	return fs.blobFeed.Subscribe(ch)
}

func (fs *FileSystem) saveBlobs(header *types.Header, blobs types.BlobSidecars) error {
	for i, b := range blobs {
		ro, err := types.NewROBlobWithRoot(types.NewBlobSidecar(b, header.Number, uint64(i)), header.Hash())
		if err != nil {
			return err
		}
		if err = fs.blobStorage.Save(types.NewVerifiedROBlob(ro)); err != nil {
			return err
		}
	}
	return nil
}

// shouldRetain checks if the blobs for a given block number should be retained based on the retention policy.
func (fs *FileSystem) shouldRetain(blockNumberRequested *big.Int) bool {
	current := fs.bc.CurrentBlock().Number
	return fs.blobStorage.WithinRetentionPeriod(filesystem.BlockNumberToEpoch(blockNumberRequested), filesystem.BlockNumberToEpoch(current))
}

// checkBlobsAvailable verifies that the provided blobs match the block's blob hashes and adheres to protocol rules.
func (fs *FileSystem) checkBlobsAvailable(block *types.Block, blobs types.BlobSidecars) error {
	if !fs.bc.Config().IsCancun(block.Number(), block.Time()) {
		if blobs != nil {
			return errors.New("sidecars present in block body before cancun")
		}
	}
	if len(blobs) > eip4844.MaxBlobsPerBlock(fs.bc.Config(), block.Time()) {
		return errors.New("too many blobs in block")
	}

	// Verify blobs match the block's blob hashes.
	var txBlobHashes [][]common.Hash
	for _, tx := range block.Transactions() {
		if tx.Type() != types.BlobTxType {
			continue
		}
		txBlobHashes = append(txBlobHashes, tx.BlobHashes())
	}
	if len(txBlobHashes) != len(blobs) {
		return errBlobNumberMismatch
	}
	if len(blobs) == 0 {
		return errNoBlobsInBlock
	}

	for i, blobHashes := range txBlobHashes {
		if err := ValidateBlobSidecar(blobHashes, blobs[i]); err != nil {
			return err
		}
	}
	return nil
}
