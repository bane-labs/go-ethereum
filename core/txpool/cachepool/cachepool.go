package cachepool

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	// ErrTxPoolCached is returned if the transaction is cached in cache pool successfully.
	// We use an error return here because we don't want a wallet nonce change after response.
	ErrTxPoolCached = errors.New("transaction cached")
)

type CachePool struct {
}

// GetCachedTransaction returns the transaction cached in pool.
func (pool *CachePool) GetCachedTransaction(nonce uint64, sender common.Address) *types.Transaction {
	// Should not be empty here.
	return nil
}
