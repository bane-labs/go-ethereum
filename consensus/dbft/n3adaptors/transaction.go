package n3adaptors

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft/block"
	"github.com/nspcc-dev/neo-go/pkg/util"
)

var _ = block.Transaction(&Transaction{})

// Transaction  is a wrapper around Eth transaction that implements block.Transaction
// interface and is sufficient for dBFT operations.
type Transaction struct {
	Tx *types.Transaction
}

// Hash implements block.Transaction interface.
func (t *Transaction) Hash() util.Uint256 {
	return util.Uint256(t.Tx.Hash())
}
