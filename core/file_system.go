package core

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
)

var (
	errSealingNumMismatch = fmt.Errorf("sealing number mismatch")
	errNoFinalizedBlock   = fmt.Errorf("no finalized block")
	errUnknownBlock       = fmt.Errorf("unknown block")
	errBlobNumberMismatch = fmt.Errorf("blob number mismatch")
)

type FileSystem struct {
	bc *BlockChain

	sealNumber   *big.Int
	pendingBlobs map[uint64]types.BlobSidecars

	blobFeed event.Feed
}

func NewFileSystem(bc *BlockChain) (*FileSystem, error) {
	fs := &FileSystem{
		bc:           bc,
		sealNumber:   nil,
		pendingBlobs: make(map[uint64]types.BlobSidecars),
	}
	go fs.loop()
	return fs, nil
}

func (fs *FileSystem) loop() {
	// Subscribe to chain head events to trigger subpool resets
	var (
		newHeadCh  = make(chan ChainHeadEvent)
		newHeadSub = fs.bc.SubscribeChainHeadEvent(newHeadCh)
	)
	defer newHeadSub.Unsubscribe()

	for range newHeadCh {
		err := fs.finalize()
		if err != nil {
			log.Error("Failed to finalize blobs", "err", err)
		}
	}
}

// finalize persists blobs for finalized blocks.
func (fs *FileSystem) finalize() error {
	finalized := fs.bc.CurrentFinalBlock()
	last := uint64(0)
	if finalized == nil {
		finalized := fs.bc.CurrentBlock()
		if finalized == nil {
			return errNoFinalizedBlock
		}
		if finalized.Number.Uint64() > 128 {
			last = finalized.Number.Uint64() - 128
		}
	} else {
		last = finalized.Number.Uint64()
	}
	for block := range fs.pendingBlobs {
		if block > last {
			continue
		}
		delete(fs.pendingBlobs, block)
	}
	return nil
}

// CommitSealBlobs stores the blobs associated with a block before sealing.
func (fs *FileSystem) CommitSealBlobs(header *types.Header, blobs types.BlobSidecars) error {
	fs.sealNumber = header.Number
	fs.pendingBlobs[header.Number.Uint64()] = blobs
	return nil
}

// CommitSealBlockHash commits the sealed block hash associated with committed blobs.
func (fs *FileSystem) CommitSealBlockHash(block *types.Block) error {
	// Deeper reorg is not supported, only consider the hash change caused by dbft multisig.
	if fs.sealNumber != block.Number() {
		return errSealingNumMismatch
	}
	blobs := fs.pendingBlobs[fs.sealNumber.Uint64()]
	var i int
	for _, tx := range block.Transactions() {
		if tx.Type() != types.BlobTxType {
			continue
		}
		if i >= len(blobs) {
			return errBlobNumberMismatch
		}
		err := ValidateBlobSidecar(tx.BlobHashes(), blobs[i])
		if err != nil {
			return err
		}
		i++
	}
	// Store blobs to local storage.
	fs.saveBlobs(block.Hash(), blobs)
	fs.blobFeed.Send(BlobEvent{
		BlockHash: block.Hash(),
		Sidecars:  blobs,
	})
	return nil
}

// GetSidecarsByRoot retrieves the blobs associated with a block hash.
// Returns nil if not found, please try to fetch from blob protocol if so.
func (fs *FileSystem) GetSidecarsByRoot(hash common.Hash) types.BlobSidecars {
	// TODO: Retrieve blobs from persistent storage with header hash.
	return nil
}

// InsertBlobs inserts blobs for a given block hash.
func (fs *FileSystem) InsertBlobs(hash common.Hash, blobs types.BlobSidecars) error {
	// Verify blobs match the block's blob hashes.
	block := fs.bc.GetBlockByHash(hash)
	if block == nil {
		return errUnknownBlock
	}
	var i int
	for _, tx := range block.Transactions() {
		if tx.Type() != types.BlobTxType {
			continue
		}
		if i >= len(blobs) {
			return errBlobNumberMismatch
		}
		err := ValidateBlobSidecar(tx.BlobHashes(), blobs[i])
		if err != nil {
			return err
		}
		i++
	}
	// Store blobs to local storage.
	fs.saveBlobs(hash, blobs)
	return nil
}

// SubscribeBlobsEvent subscribes to blob events.
func (fs *FileSystem) SubscribeBlobsEvent(ch chan<- BlobEvent) event.Subscription {
	return fs.blobFeed.Subscribe(ch)
}

func (fs *FileSystem) saveBlobs(hash common.Hash, blobs types.BlobSidecars) error {
	return nil
}
