package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft"
)

var _ dbft.PreBlock[common.Hash] = (*PreBlock)(nil)

// PreBlock is a wrapper around Eth block that implements [dbft.PreBlock] interface.
// It holds some initial proposed block's data as far as standard/encrypted
// transactions.
type PreBlock struct {
	header       *types.Header
	withdrawals  []*types.Withdrawal
	transactions []*types.Transaction
	localSecret  []byte
}

// Data implements [dbft.PreBlock] interface.
func (p *PreBlock) Data() []byte { return p.localSecret }

// SetData implements [dbft.PreBlock] interface.
func (p *PreBlock) SetData(_ dbft.PrivateKey) error {
	// TODO: here we need to set validator's own part of shared key for this block (localSecret).
	return nil
}

// Verify implements [dbft.PreBlock] interface.
func (p *PreBlock) Verify(_ dbft.PublicKey, _ []byte) error {
	// TODO: in this method we should verify that provided part of shared key is valid.
	return nil
}

// Transactions implements [dbft.PreBlock] interface.
func (b *PreBlock) Transactions() []dbft.Transaction[common.Hash] {
	dst := make([]dbft.Transaction[common.Hash], len(b.transactions))
	for i, tx := range b.transactions {
		dst[i] = &Transaction{
			Tx: tx,
		}
	}
	return dst
}

// SetTransactions implements [dbft.PreBlock] interface. txx may contain encrypted
// Envelope transactions.
func (b *PreBlock) SetTransactions(txx []dbft.Transaction[common.Hash]) {
	txs := make([]*types.Transaction, len(txx))
	for i, tx := range txx {
		txs[i] = tx.(*Transaction).Tx
	}
	b.transactions = txs
}

// ToEthBlock converts [dbft.PreBlock] to [types.Block].
func (b *PreBlock) ToEthBlock() *types.Block {
	res := types.NewBlockWithHeader(b.header)
	// Uncles are always nil in dBFT-like consensus.
	res = res.WithBody(b.transactions, nil).WithWithdrawals(b.withdrawals)
	return res
}
