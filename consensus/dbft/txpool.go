package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// txPool defines the methods needed from a transaction pool implementation to
// support all the operations needed by the Ethereum chain protocols.
type txPool interface {
	// Has returns an indicator whether txpool has a transaction
	// cached with the given hash.
	Has(hash common.Hash) bool

	// Get retrieves the transaction from local txpool with given
	// tx hash.
	Get(hash common.Hash) *types.Transaction

	// Add should add the given transactions to the pool.
	Add(txs []*types.Transaction, local bool, sync bool) []error

	// Pending should return pending transactions.
	// The slice should be modifiable by the caller.
	Pending(enforceTips bool) map[common.Address][]*txpool.LazyTransaction

	// SubscribeTransactions should return an event subscription of
	// NewTxsEvent and send events to the given channel. It supports
	// feeding only newly seen or also resurrected transactions.
	SubscribeTransactions(ch chan<- core.NewTxsEvent, reorgs bool) event.Subscription
}
