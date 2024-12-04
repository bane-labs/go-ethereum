package tpke

import (
	"fmt"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

const (
	// SignatureLen is the size of Signature in the serialized representation.
	SignatureLen = bls12381.SizeOfG2AffineCompressed
	// SignatureShareLen is the size of SignatureShare in the serialized
	// representation.
	SignatureShareLen = bls12381.SizeOfG2AffineCompressed
)

// Domain is a BLS12-381 domain separation tag used during message hashing.
var Domain = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")

// Signature is a BLS12-381 based signature implementation used in TPKE scheme.
type Signature struct {
	pg2 *bls12381.G2Affine
}

// NewSignature returns new Signature constructed from the provided point.
func NewSignature(pg2 *bls12381.G2Affine) *Signature {
	return &Signature{
		pg2: pg2,
	}
}

// NewSignatureFromBytes deserializes Signature from the given byte slice. It expects
// the input slice to have [SignatureLen] length.
func NewSignatureFromBytes(b []byte) (*Signature, error) {
	if len(b) != SignatureLen {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrTPKEDecoding, SignatureLen, len(b))
	}
	s := &Signature{
		pg2: new(bls12381.G2Affine),
	}
	_, err := s.pg2.SetBytes(b)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Equals returns whether two signatures are equal.
func (s *Signature) Equals(sig *Signature) bool {
	return s.pg2.Equal(sig.pg2)
}

// Bytes encodes a Signature into a byte slice with the length of [SignatureLen].
func (s *Signature) Bytes() []byte {
	b := s.pg2.Bytes()
	return b[:]
}

// SignatureShare is a BLS12-381 point used for BLS aggregation. In its essence it's
// the same type as Signature.
type SignatureShare struct {
	pg2 *bls12381.G2Affine
}

// NewSignatureShareFromBytes deserializes SignatureShare from the given byte slice.
// It expects the input slice to have [SignatureShareLen] length.
func NewSignatureShareFromBytes(b []byte) (*SignatureShare, error) {
	if len(b) != SignatureShareLen {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrTPKEDecoding, SignatureShareLen, len(b))
	}
	s := &SignatureShare{
		pg2: new(bls12381.G2Affine),
	}
	_, err := s.pg2.SetBytes(b)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Bytes encodes a SignatureShare into a byte slice of [SignatureShareLen] length.
func (s *SignatureShare) Bytes() []byte {
	b := s.pg2.Bytes()
	return b[:]
}

// AggregateSigShares tries to aggregate SignatureShare to a BLS signature.
// This method takes a slice of [SignatureShare] and a matrix for Feldman.
// The size of [SignatureShare] slice should be equal to len(message).
// Each row of the input matrix should be [1, i, i^2, ..., i^(threshold-1)], where i
// is the dkg key index. The message amount should be larger than threshold,
// otherwise the result will be wrong.
func AggregateSigShares(matrix [][]int, shares []*SignatureShare, scaler int) (*Signature, error) {
	if len(matrix) != len(shares) {
		return nil, ErrTPKELengthMismatch
	}
	// Be aware of the integer overflow when the size and threshold grow big
	d, coeff := Feldman(matrix)
	d = scaler / d
	// Compute d1
	denominator := big.NewInt(int64(abs(d)))
	pg2 := new(bls12381.G2Affine)
	// Add up shares with some factors as d1
	for i := 0; i < len(shares); i++ {
		minor := new(bls12381.G2Affine).ScalarMultiplication(shares[i].pg2, big.NewInt(int64(abs(coeff[i]))))
		if coeff[i] < 0 {
			minor.Neg(minor)
		}
		pg2.Add(pg2, minor)
	}
	// Divide d1 by d
	pg2.ScalarMultiplication(pg2, denominator)
	// Verify
	return NewSignature(pg2), nil
}
