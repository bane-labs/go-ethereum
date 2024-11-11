package tpke

import (
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

// SignatureLen is the size of Signature in the serialized representation.
const SignatureLen = bls12381.SizeOfG2AffineCompressed

var Domain = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")

type Signature struct {
	pg2 *bls12381.G2Affine
}

func NewSignature(pg2 *bls12381.G2Affine) *Signature {
	return &Signature{
		pg2: pg2,
	}
}

func (s *Signature) Equals(sig *Signature) bool {
	return s.pg2.Equal(sig.pg2)
}

// ToBytes encodes a Signature into a byte array whose length is 96
func (s *Signature) ToBytes() []byte {
	b := s.pg2.Bytes()
	return b[:]
}

// FromBytes decodes a 96-byte array as a Signature
func (s *Signature) FromBytes(b []byte) (*Signature, error) {
	if len(b) != 96 {
		return nil, ErrTPKEDecoding
	}
	pg2 := new(bls12381.G2Affine)
	_, err := pg2.SetBytes(b)
	if err != nil {
		return nil, err
	}
	s.pg2 = pg2
	return s, nil
}

// SignatureShare is the same as Signature, but used for BLS aggregation
type SignatureShare struct {
	pg2 *bls12381.G2Affine
}

// ToBytes encodes a SignatureShare into a byte array whose length is 96
func (s *SignatureShare) ToBytes() []byte {
	b := s.pg2.Bytes()
	return b[:]
}

// FromBytes decodes a 96-byte array as a SignatureShare
func (s *SignatureShare) FromBytes(b []byte) (*SignatureShare, error) {
	pg2 := new(bls12381.G2Affine)
	_, err := pg2.SetBytes(b)
	if err != nil {
		return nil, err
	}
	s.pg2 = pg2
	return s, nil
}

// AggregateSigShares tries to aggregate SignatureShare to a BLS signature
// This method takes a batch of SignatureShares and a matrix for Feldman
// The size of SignatureShare array should be len(message)
// Each row of the input matrix should be [1, i, i^2, ..., i^(threshold-1)], i is the dkg key index
// The message amount should be larger than threshold, otherwise the result will be wrong
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
