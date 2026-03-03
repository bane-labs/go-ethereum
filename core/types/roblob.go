package types

import (
	"errors"
	"math/big"
)

const BlobSidecarSize = 131190 // defined to match blob sidecar size in rlp codegen

type BlobSidecar struct {
	BlobTxSidecar
	BlockNumber *big.Int // The block number this blob sidecar is associated with.
	BlockTime   uint64   // The block time this blob sidecar is associated with.
	Index       uint64   // The index of this blob sidecar within the block.
}

// NewBlobSidecar creates a new BlobSidecar instance.
func NewBlobSidecar(b *BlobTxSidecar, blockNumber *big.Int, blockTime uint64, index uint64) *BlobSidecar {
	if b == nil || blockNumber == nil {
		return nil
	}
	return &BlobSidecar{
		BlobTxSidecar: *b,
		BlockNumber:   blockNumber,
		BlockTime:     blockTime,
		Index:         index,
	}
}

// ROBlob represents a read-only blob sidecar in block with its block root.
type ROBlob struct {
	*BlobSidecar
	root [32]byte // The root of the block this blob sidecar is associated with.
}

// NewROBlobWithRoot creates a new ROBlob with a given root.
func NewROBlobWithRoot(b *BlobSidecar, root [32]byte) (ROBlob, error) {
	if b == nil {
		return ROBlob{}, errors.New("received nil blob sidecar")
	}
	return ROBlob{BlobSidecar: b, root: root}, nil
}

// BlockRoot returns the root of the block.
func (b *ROBlob) BlockRoot() [32]byte {
	return b.root
}

// BlockRootSlice returns the block root as a byte slice. This is often more convenient/concise
// than setting a tmp var to BlockRoot(), just so that it can be sliced.
func (b *ROBlob) BlockRootSlice() []byte {
	return b.root[:]
}

// VerifiedROBlob represents an ROBlob that has undergone full verification (eg block sig, inclusion proof, commitment check).
type VerifiedROBlob struct {
	ROBlob
}

// NewVerifiedROBlob "upgrades" an ROBlob to a VerifiedROBlob. This method should only be used by the verification package.
func NewVerifiedROBlob(rob ROBlob) VerifiedROBlob {
	return VerifiedROBlob{ROBlob: rob}
}
