package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft"
)

var _ = dbft.Transaction[common.Hash](&Transaction{})

// Transaction  is a wrapper around Eth transaction that implements block.Transaction
// interface and is sufficient for dBFT operations.
type Transaction struct {
	Tx *types.Transaction
}

// Hash implements block.Transaction interface.
func (t *Transaction) Hash() common.Hash {
	return t.Tx.Hash()
}
