package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
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
	localShares  []byte

	// envelopesCount is the cached number of Envelopes in the proposed PreBlock.
	envelopesCount int
	// finalTransactions is the cached final list of transactions formed after TPKE
	// decryption of Envelopes content. This list includes both simple standard and
	// decrypted transactions.
	finalTransactions []*types.Transaction
}

// Data implements [dbft.PreBlock] interface.
func (p *PreBlock) Data() []byte { return p.localShares }

// SetData implements [dbft.PreBlock] interface.
func (p *PreBlock) SetData(pk dbft.PrivateKey) error {
	encryptedTxs := decodeEnvelopesData(p.transactions)
	var encryptedKeys []*tpke.CipherText
	for i := range encryptedTxs {
		encryptedKeys = append(encryptedKeys, encryptedTxs[i].encryptedKey)
	}
	shares, err := pk.(*Signer).AmevKeystore.DecryptWithShare(encryptedKeys)
	if err != nil {
		return fmt.Errorf("failed to construct shares: %w", err)
	}

	p.localShares = encodeShares(shares)
	return nil
}

// Verify implements [dbft.PreBlock] interface.
func (p *PreBlock) Verify(_ dbft.PublicKey, data []byte) error {
	shares, err := decodeShares(data)
	if err != nil {
		return fmt.Errorf("decode shares: %w", err)
	}
	p.calculateEnvelopes()
	if len(shares) != p.envelopesCount {
		return fmt.Errorf("invalid envelopes count: expected %d, got %d", p.envelopesCount, len(shares))
	}
	return nil
}

// calculateEnvelopes calculates the number of Envelope transactions in the proposed
// PreBlock and caches resulting value. It's not thread-safe and aimed to be used in
// dBFT callbacks only.
func (p *PreBlock) calculateEnvelopes() {
	if p.envelopesCount == -1 {
		p.envelopesCount = 0
		for i := range p.transactions {
			if isEnvelope(p.transactions[i]) {
				p.envelopesCount++
			}
		}
	}
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
