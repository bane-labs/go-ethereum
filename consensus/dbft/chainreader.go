package dbft

import (
	"context"

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
	Genesis() *types.Block
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
	HasBlock(hash common.Hash, number uint64) bool
	GetBlock(hash common.Hash, number uint64) *types.Block
	GetBlockByNumber(uint64) *types.Block
	StateAt(header *types.Header) (*state.StateDB, error)
	VerifyBlock(block *types.Block, checkState bool) (*state.StateDB, *core.ProcessResult, error)
	ProcessState(ctx context.Context, block *types.Block, statedb *state.StateDB) (*state.StateDB, *core.ProcessResult, error)

	// Only for EVM context construction
	Engine() consensus.Engine
}

// ChainInsertFn is a callback type to insert a block into the local chain.
type ChainInsertFn func(*types.Block) error

// SyncingFn is a callback type to check whether the node is syncing.
type SyncingFn func() bool
