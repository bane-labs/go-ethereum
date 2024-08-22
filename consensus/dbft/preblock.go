package dbft

import (
	"encoding/binary"
	"errors"
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
	data, err := EncodeShares(shares)
	if err != nil {
		return fmt.Errorf("failed to encode shares: %w", err)
	}
	p.localShares = data
	return nil
}

// TODO: replace this constant with the one from crypto/tpke/encryption.go
const shareLen = 48

// TODO: move encoder/decoder to RLP scheme.
func EncodeShares(shares []*tpke.DecryptionShare) ([]byte, error) {
	var res = make([]byte, 4, shareLen*len(shares)+4)
	binary.LittleEndian.PutUint32(res, uint32(len(shares)))
	for i := range shares {
		res = append(res, shares[i].ToBytes()...)
	}
	return res, nil
}

func DecodeShares(buf []byte) ([]*tpke.DecryptionShare, error) {
	if len(buf) < 4 {
		return nil, errors.New("decryption shares slice is too short")
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	res := make([]*tpke.DecryptionShare, n)
	for i := range res {
		res[i] = &tpke.DecryptionShare{}
		res[i].FromBytes(buf[4+i*shareLen : 4+(i+1)*shareLen])
	}
	return res, nil
}

// Verify implements [dbft.PreBlock] interface.
func (p *PreBlock) Verify(_ dbft.PublicKey, _ []byte) error {
	// TODO: in this method we should verify that provided part of shared key
	// (shares received from other CNs) is valid. But we can't easily do this
	// because for shares verification we need at least M shares (whereas this method
	// is called for a single PreCommit), and even with M shares if some of them is
	// invalid, we don't know which one. Thus, here we can check only serialization
	// format, and the rest goes to the Block constructor level. This problem also
	// requires dBFT modification, because if M shares can't properly decrypt transactions
	// then we need to collect more shares from other CNs, ref.
	// https://github.com/bane-labs/go-ethereum/pull/301#discussion_r1726514210.
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
