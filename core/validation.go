package core

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
)

var (
	// ErrKZGVerificationError is returned when a KZG proof was not verified correctly.
	ErrKZGVerificationError = errors.New("KZG verification error")
)

func ValidateBlobSidecar(sidecar *types.BlobTxSidecar, hashes []common.Hash) error {
	if len(sidecar.Blobs) != len(hashes) {
		return fmt.Errorf("invalid number of %d blobs compared to %d blob hashes", len(sidecar.Blobs), len(hashes))
	}
	if err := sidecar.ValidateBlobCommitmentHashes(hashes); err != nil {
		return err
	}
	// Fork-specific sidecar checks, including proof verification.
	if sidecar.Version == types.BlobSidecarVersion1 {
		return validateBlobSidecarOsaka(sidecar, hashes)
	} else {
		return validateBlobSidecarLegacy(sidecar, hashes)
	}
}

func validateBlobSidecarLegacy(sidecar *types.BlobTxSidecar, hashes []common.Hash) error {
	if len(sidecar.Proofs) != len(hashes) {
		return fmt.Errorf("invalid number of %d blob proofs expected %d", len(sidecar.Proofs), len(hashes))
	}
	for i := range sidecar.Blobs {
		if err := kzg4844.VerifyBlobProof(&sidecar.Blobs[i], sidecar.Commitments[i], sidecar.Proofs[i]); err != nil {
			return fmt.Errorf("%w: invalid blob proof: %v", ErrKZGVerificationError, err)
		}
	}
	return nil
}

func validateBlobSidecarOsaka(sidecar *types.BlobTxSidecar, hashes []common.Hash) error {
	if len(sidecar.Proofs) != len(hashes)*kzg4844.CellProofsPerBlob {
		return fmt.Errorf("invalid number of %d blob proofs expected %d", len(sidecar.Proofs), len(hashes)*kzg4844.CellProofsPerBlob)
	}
	if err := kzg4844.VerifyCellProofs(sidecar.Blobs, sidecar.Commitments, sidecar.Proofs); err != nil {
		return fmt.Errorf("%w: %v", ErrKZGVerificationError, err)
	}
	return nil
}
