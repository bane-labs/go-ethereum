package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft/block"
	"github.com/nspcc-dev/dbft/crypto"
	"github.com/nspcc-dev/neo-go/pkg/util"
)

var _ block.Block = (*Block)(nil)

// NsInS is the number of nanoseconds in second.
const NsInS = 1000_000_000

// Block is a wrapper around Eth block that implements block.Block interface and is
// sufficient for dBFT operations.
type Block struct {
	*types.Block
	signatureBytes []byte
}

// Version implements block.Block interface.
func (b *Block) Version() uint32 {
	// Currently there's no free space for Version in the "shared" Eth-N3 block.
	panic("TODO")
}

// PrevHash implements block.Block interface.
func (b *Block) PrevHash() util.Uint256 {
	return b.ParentHash().Uint256()
}

// Timestamp implements block.Block interface.
func (b *Block) Timestamp() uint64 {
	return b.Time() * NsInS
}

// Index implements block.Block interface.
func (b *Block) Index() uint32 {
	return uint32(b.NumberU64())
}

// NextConsensus implements block.Block interface.
func (b *Block) NextConsensus() (u util.Uint160) {
	copy(u[:], b.MixDigest().Bytes()[common.HashLength-util.Uint160Size:])
	return
}

// MerkleRoot implements block.Block interface.
func (b *Block) MerkleRoot() util.Uint256 {
	return b.Root().Uint256()
}

// ConsensusData implements block.Block interface.
func (b *Block) ConsensusData() uint64 {
	panic("TODO")
}

// Transactions implements block.Block interface.
func (b *Block) Transactions() []block.Transaction {
	src := b.Body().Transactions
	dst := make([]block.Transaction, len(src))
	for i, tx := range src {
		dst[i] = &Transaction{
			Tx: tx,
		}
	}
	return dst
}

// SetTransactions implements block.Block interface. It changes the underlying
// Block.
func (b *Block) SetTransactions(txx []block.Transaction) {
	txs := make([]*types.Transaction, len(txx))
	for i, tx := range txx {
		txs[i] = tx.(*Transaction).Tx
	}
	b.Block = b.Block.WithBody(txs, nil) // Uncles are always nil in Clique-like consensus.
}

// Signature implements Block interface.
func (b *Block) Signature() []byte {
	return b.signatureBytes
}

// Sign implements Block interface.
func (b *Block) Sign(key crypto.PrivateKey) error {
	sighash, err := key.Sign(dbftRLP(b.Header()))
	if err != nil {
		return fmt.Errorf("failed to sign dbftRLP header: %w", err)
	}

	b.signatureBytes = sighash
	return nil
}

// Verify implements Block interface.
func (b *Block) Verify(pub crypto.PublicKey, sign []byte) error {
	panic("TODO")
}

// Hash implements Block interface. Hash returns unsealed block hash that doesn't
// include Nonce, MixDigest fields and Extra's signature part, thus, can be used
// only for worker's block identification and information purposes.
func (b *Block) Hash() util.Uint256 {
	return WorkerSealHash(b.Header()).Uint256()
}
