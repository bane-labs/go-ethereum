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

	// envelopesData is the ordered list of decoded content of Envelopes
	// transactions of the proposed PreBlock.
	envelopesData []envelopeData
	// finalTransactions is the cached final list of transactions formed after TPKE
	// decryption of Envelopes content. This list includes both simple standard and
	// decrypted transactions.
	finalTransactions []*types.Transaction
}

// Data implements [dbft.PreBlock] interface.
func (p *PreBlock) Data() []byte { return p.localShares }

// SetData implements [dbft.PreBlock] interface.
func (p *PreBlock) SetData(pk dbft.PrivateKey) error {
	encryptedKeys := make([]*tpke.CipherText, len(p.envelopesData))
	for i := range p.envelopesData {
		encryptedKeys[i] = p.envelopesData[i].encryptedKey
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
	if len(shares) != len(p.envelopesData) {
		return fmt.Errorf("invalid Envelopes count: expected %d, got %d", len(p.envelopesData), len(shares))
	}
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
	var (
		txs       = make([]*types.Transaction, len(txx))
		envelopes []envelopeData // don't allocate, Envelopes supposed to be rare.
	)
	for i, tx := range txx {
		txs[i] = tx.(*Transaction).Tx
		if isEnvelope(txs[i]) {
			d, err := decodeEnvelopeData(txs[i].Data())
			if err != nil {
				// Not an Envelope in fact since it contains malformed data. Include
				// it as a simple transaction.
				continue
			}
			d.index = i
			envelopes = append(envelopes, d)
		}
	}
	b.transactions = txs
	b.envelopesData = envelopes
}

// ToEthBlock converts [dbft.PreBlock] to [types.Block].
func (b *PreBlock) ToEthBlock() *types.Block {
	res := types.NewBlockWithHeader(b.header)
	// Uncles are always nil in dBFT-like consensus.
	res = res.WithBody(b.transactions, nil).WithWithdrawals(b.withdrawals)
	return res
}
