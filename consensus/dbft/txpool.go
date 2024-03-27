package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// txPool defines the methods needed from a transaction pool implementation to
// support all the operations needed by the Ethereum chain protocols.
type txPool interface {
	// Get retrieves the transaction from local txpool with given
	// tx hash.
	Get(hash common.Hash) *types.Transaction
}
