package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// ChainHeaderReader is a Blockchain API abstraction needed for proper dBFT work.
type ChainHeaderReader interface {
	consensus.ChainHeaderReader
	CurrentBlock() *types.Header
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	HasBlock(hash common.Hash, number uint64) bool
	GetBlockByNumber(uint64) *types.Block
	VerifyBlock(block *types.Block) error
	ProcessState(block *types.Block) (*state.StateDB, types.Receipts, []*types.Log, uint64, error)
}

// ChainHeaderWriter is a Blockchain API abstraction needed for proper blockQueue
// work.
type ChainHeaderWriter interface {
	ChainHeaderReader
	InsertChain(chain types.Blocks) (int, error)
	InsertBlockWithoutSetHead(b *types.Block) error
}
