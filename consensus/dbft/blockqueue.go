package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
)

// blockQueue is an entity that collects sealed blocks from dBFT and stores these
// blocks in the chain.
type blockQueue struct {
	chain ChainHeaderWriter
	feed  *event.Feed
}

// newBlockQueue creates an instance of blockQueue. It's not ready for usage until
// an instance of ChainHeaderWriter is properly set.
func newBlockQueue(feed *event.Feed) *blockQueue {
	return &blockQueue{
		feed: feed,
	}
}

// SetChain initializes ChainHeaderWriter instanse needed for proper blockQueue
// functioning.
func (bq *blockQueue) SetChain(chain ChainHeaderWriter) {
	bq.chain = chain
}

// PutBlock routs block either to miner or (if there's no suitable sealing task)
// directly to blockchain. No block verification is performed, it is assumed that
// provided block is sealed and valid.
func (bq *blockQueue) PutBlock(b *types.Block, state *state.StateDB, receipts []*types.Receipt) error {
	hash := b.Hash()
	if bq.chain.HasBlock(hash, b.NumberU64()) {
		return nil
	}

	// Short circuit if we don't have pre-calculated state (it's possible only in case of
	// reorg if dBFT tries to insert new parent during PrepareRequest construction).
	if state == nil {
		_, err := bq.chain.InsertChain(types.Blocks{b})
		if err != nil {
			return fmt.Errorf("failed to insert block into chain: %w", err)
		}
		log.Info("Successfully inserted new block", "number", b.Number(), "hash", hash)

		// Broadcast the block and announce chain insertion event
		bq.feed.Send(b)
		return nil
	}

	currH := bq.chain.CurrentBlock().Number
	// Insert state directly if we have one.
	var logs []*types.Log
	for i, receipt := range receipts {
		// Add block location fields.
		receipt.BlockHash = hash
		receipt.BlockNumber = b.Number()
		receipt.TransactionIndex = uint(i)

		// Update the block hash in all logs since it is now available and not when the
		// receipt/log of individual transactions were created.
		for _, taskLog := range receipt.Logs {
			taskLog.BlockHash = hash
		}
		logs = append(logs, receipt.Logs...)
	}
	// Commit block and state to database.
	_, err := bq.chain.WriteBlockAndSetHead(b, receipts, logs, state, b.Number().Cmp(currH) > 0)
	if err != nil {
		log.Error("Failed to write block to chain and set head",
			"number", b.NumberU64(),
			"err", err)
		return fmt.Errorf("failed to write block into chain: %w", err)
	}
	log.Info("Successfully wrote new block with state", "number", b.Number(), "hash", hash)

	// Broadcast the block and announce chain insertion event
	bq.feed.Send(b)
	return nil
}
