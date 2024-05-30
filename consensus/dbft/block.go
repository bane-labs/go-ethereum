package dbft

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/nspcc-dev/dbft"
)

var _ dbft.Block[common.Hash] = (*Block)(nil)

// NsInS is the number of nanoseconds in second.
const NsInS = 1000_000_000

// Block is a wrapper around Eth block that implements block.Block interface and is
// sufficient for dBFT operations.
type Block struct {
	header              *types.Header
	withdrawals         []*types.Withdrawal
	transactions        []*types.Transaction
	localSignatureBytes []byte

	// Local data calculated during dBFT block verification. Allowed to be empty.
	state    *state.StateDB
	receipts types.Receipts
}

// PrevHash implements block.Block interface.
func (b *Block) PrevHash() common.Hash {
	return b.header.ParentHash
}

// Timestamp implements block.Block interface.
func (b *Block) Timestamp() uint64 {
	return b.header.Time * NsInS
}

// Index implements block.Block interface.
func (b *Block) Index() uint32 {
	return uint32(b.header.Number.Uint64())
}

// MerkleRoot implements block.Block interface.
func (b *Block) MerkleRoot() common.Hash {
	return b.header.Root
}

// Transactions implements block.Block interface.
func (b *Block) Transactions() []dbft.Transaction[common.Hash] {
	dst := make([]dbft.Transaction[common.Hash], len(b.transactions))
	for i, tx := range b.transactions {
		dst[i] = &Transaction{
			Tx: tx,
		}
	}
	return dst
}

// SetTransactions implements block.Block interface. It changes the underlying
// Block.
func (b *Block) SetTransactions(txx []dbft.Transaction[common.Hash]) {
	txs := make([]*types.Transaction, len(txx))
	for i, tx := range txx {
		txs[i] = tx.(*Transaction).Tx
	}
	b.transactions = txs
}

// Signature implements Block interface.
func (b *Block) Signature() []byte {
	return b.localSignatureBytes
}

// Sign implements Block interface.
func (b *Block) Sign(key dbft.PrivateKey) error {
	sighash, err := key.Sign(dbftRLP(b.header))
	if err != nil {
		return fmt.Errorf("failed to sign dbftRLP header: %w", err)
	}

	b.localSignatureBytes = sighash
	return nil
}

// Verify implements Block interface.
func (b *Block) Verify(pub dbft.PublicKey, sign []byte) error {
	sealHash := HonestSealHash(b.header)
	pubkey, err := ecrypto.Ecrecover(sealHash.Bytes(), sign)
	if err != nil {
		return fmt.Errorf("failed to recover public key from signature: %w", err)
	}
	if pub.(*PublicKey).Account != ecrypto.PubkeyBytesToAddress(pubkey) {
		return errors.New("invalid block signature")
	}
	return nil
}

// Hash implements Block interface. Hash returns unsealed block hash that doesn't
// include Nonce, MixDigest fields and Extra's signature part, thus, can be used
// only for worker's block identification and information purposes.
func (b *Block) Hash() common.Hash {
	return WorkerSealHash(b.header)
}

// ToEthBlock converts [dbft.Block] to [types.Block].
func (b *Block) ToEthBlock() *types.Block {
	res := types.NewBlockWithHeader(b.header)
	// Uncles are always nil in dBFT-like consensus.
	res = res.WithBody(b.transactions, nil).WithWithdrawals(b.withdrawals)
	return res
}
