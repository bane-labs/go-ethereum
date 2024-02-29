package dbft

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"
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
	header              *types.Header
	withdrawals         []*types.Withdrawal
	transactions        []*types.Transaction
	localSignatureBytes []byte
}

// Version implements block.Block interface.
func (b *Block) Version() uint32 {
	// Currently there's no free space for Version in the "shared" Eth-N3 block.
	panic("TODO")
}

// PrevHash implements block.Block interface.
func (b *Block) PrevHash() util.Uint256 {
	return b.header.ParentHash.Uint256()
}

// Timestamp implements block.Block interface.
func (b *Block) Timestamp() uint64 {
	return b.header.Time * NsInS
}

// Index implements block.Block interface.
func (b *Block) Index() uint32 {
	return uint32(b.header.Number.Uint64())
}

// NextConsensus implements block.Block interface.
func (b *Block) NextConsensus() (u util.Uint160) {
	copy(u[:], b.header.MixDigest.Bytes()[common.HashLength-util.Uint160Size:])
	return
}

// MerkleRoot implements block.Block interface.
func (b *Block) MerkleRoot() util.Uint256 {
	return b.header.Root.Uint256()
}

// ConsensusData implements block.Block interface.
func (b *Block) ConsensusData() uint64 {
	panic("TODO")
}

// Transactions implements block.Block interface.
func (b *Block) Transactions() []block.Transaction {
	dst := make([]block.Transaction, len(b.transactions))
	for i, tx := range b.transactions {
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
	b.transactions = txs
}

// Signature implements Block interface.
func (b *Block) Signature() []byte {
	return b.localSignatureBytes
}

// Sign implements Block interface.
func (b *Block) Sign(key crypto.PrivateKey) error {
	sighash, err := key.Sign(dbftRLP(b.header))
	if err != nil {
		return fmt.Errorf("failed to sign dbftRLP header: %w", err)
	}

	b.localSignatureBytes = sighash
	return nil
}

// Verify implements Block interface.
func (b *Block) Verify(pub crypto.PublicKey, sign []byte) error {
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
func (b *Block) Hash() util.Uint256 {
	return WorkerSealHash(b.header).Uint256()
}

// ToEthBlock converts [dbft.Block] to [types.Block].
func (b *Block) ToEthBlock() *types.Block {
	res := types.NewBlockWithHeader(b.header)
	// Uncles are always nil in dBFT-like consensus.
	res = res.WithBody(b.transactions, nil)
	if b.withdrawals != nil {
		res = res.WithWithdrawals(b.withdrawals)
	}
	return res
}
