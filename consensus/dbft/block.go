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

// Block is a wrapper around Eth block that implements [dbft.Block] interface and is
// sufficient for dBFT operations.
type Block struct {
	header              *types.Header
	withdrawals         []*types.Withdrawal
	transactions        []*types.Transaction
	localSignatureBytes []byte

	// Local data got after [dbft.Block] construction. Always non-nil in a properly
	// constructed Block.
	state    *state.StateDB
	receipts types.Receipts
}

// PrevHash implements [dbft.Block] interface.
func (b *Block) PrevHash() common.Hash {
	return b.header.ParentHash
}

// Timestamp implements [dbft.Block] interface.
func (b *Block) Timestamp() uint64 {
	return b.header.Time * NsInS
}

// Index implements [dbft.Block] interface.
func (b *Block) Index() uint32 {
	return uint32(b.header.Number.Uint64())
}

// MerkleRoot implements [dbft.Block] interface.
func (b *Block) MerkleRoot() common.Hash {
	return b.header.Root
}

// Transactions implements [dbft.Block] interface.
func (b *Block) Transactions() []dbft.Transaction[common.Hash] {
	dst := make([]dbft.Transaction[common.Hash], len(b.transactions))
	for i, tx := range b.transactions {
		dst[i] = &Transaction{
			Tx: tx,
		}
	}
	return dst
}

// SetTransactions implements [dbft.Block] interface. It does not change the
// underlying block.
func (b *Block) SetTransactions(txx []dbft.Transaction[common.Hash]) {
	// TODO: this callback is a Block's finalizer, and it's likely to be empty.
	// Block's transactions should be finalized earlier in NewBlockFromContext
	// and are present by this moment.
}

// Signature implements [dbft.Block] interface.
func (b *Block) Signature() []byte {
	return b.localSignatureBytes
}

// Sign implements [dbft.Block] interface.
func (b *Block) Sign(key dbft.PrivateKey) error {
	sighash, err := key.Sign(dbftRLP(b.header))
	if err != nil {
		return fmt.Errorf("failed to sign dbftRLP header: %w", err)
	}

	b.localSignatureBytes = sighash
	return nil
}

// Verify implements [dbft.Block] interface.
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

// Hash implements [dbft.Block] interface. Hash returns unsealed block hash that doesn't
// include Nonce, MixDigest fields and Extra's signature part, thus, can be used
// only for worker's block identification and information purposes.
func (b *Block) Hash() common.Hash {
	return WorkerSealHash(b.header)
}
