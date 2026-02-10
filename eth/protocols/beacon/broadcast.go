package beacon

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// blockPropagation is a block propagation event, waiting for its turn in the
// broadcast queue.
type blockPropagation struct {
	block *types.Block
	td    *big.Int
}

// broadcastBlocks is a write loop that multiplexes blocks and block announcements
// to the remote peer. The goal is to have an async writer that does not lock up
// node internals and at the same time rate limits queued data.
func (p *Peer) broadcastBlocks() {
	for {
		select {
		case prop := <-p.queuedBlocks:
			if err := p.SendNewBlock(prop.block, prop.td); err != nil {
				return
			}
			p.Log().Trace("Propagated block", "number", prop.block.Number(), "hash", prop.block.Hash(), "td", prop.td)

		case block := <-p.queuedBlockAnns:
			if err := p.SendNewBlockHashes([]common.Hash{block.Hash()}, []uint64{block.NumberU64()}); err != nil {
				return
			}
			p.Log().Trace("Announced block", "number", block.Number(), "hash", block.Hash())

		case <-p.term:
			return
		}
	}
}

// broadcastBlockBlob is a write loop that schedules blob broadcasts
// to the remote peer. The goal is to have an async writer that does not lock up
// node internals and at the same time rate limits queued data.
func (p *Peer) broadcastBlockBlob() {
	for {
		select {
		case blockHash := <-p.blobRootBroadcast:
			if err := p.sendNewBlobsRoot(blockHash); err != nil {
				return
			}
			p.Log().Trace("Sent blobs block hash", "block hash", blockHash)
		case <-p.term:
			return
		}
	}
}
