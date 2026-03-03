package dbft

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
)

// blockQueue is an entity that collects sealed blocks from dBFT and stores these
// blocks in the chain.
type blockQueue struct {
	chain       ChainHeaderReader
	fs          FSWriter
	feed        *event.Feed
	insertChain ChainInsertFn
}

// newBlockQueue creates an instance of blockQueue. It's not ready for usage until
// an instance of ChainHeaderWriter is properly set.
func newBlockQueue(feed *event.Feed) *blockQueue {
	return &blockQueue{
		feed: feed,
	}
}

// SetChain initializes ChainHeaderReader instanse needed for proper blockQueue
// functioning.
func (bq *blockQueue) SetChain(chain ChainHeaderReader, inserter ChainInsertFn) {
	bq.chain = chain
	bq.insertChain = inserter
}

// SetFileSystem initializes FSWriter instanse needed for proper blockQueue functioning.
func (bq *blockQueue) SetFileSystem(fs FSWriter) {
	bq.fs = fs
}

// PutBlock routes block to blockchain. No block verification is performed, it is
// assumed that provided block is sealed and valid.
func (bq *blockQueue) PutBlock(b *types.Block) error {
	hash := b.Hash()
	if bq.chain.HasBlock(hash, b.NumberU64()) {
		return nil
	}

	if err := bq.fs.CommitSealBlockHash(b); err != nil {
		log.Error("Failed to commit seal block hash into filesystem", "number", b.NumberU64(), "hash", hash, "err", err)
		return err
	}

	if err := bq.insertChain(b); err != nil {
		return err
	}

	// Broadcast the block and announce chain insertion event
	bq.feed.Send(b)
	return nil
}
