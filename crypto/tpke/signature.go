package tpke

import (
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

var Domain = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")

type Signature struct {
	pg2 *bls12381.PointG2
}

func NewSignature(pg2 *bls12381.PointG2) *Signature {
	return &Signature{
		pg2: pg2,
	}
}

func (s *Signature) Equals(sig *Signature) bool {
	g2 := bls12381.NewG2()
	return g2.Equal(s.pg2, sig.pg2)
}

// ToBytes encodes a Signature into a byte array whose length is 96
func (s *Signature) ToBytes() []byte {
	return bls12381.NewG2().ToCompressed(s.pg2)
}

// FromBytes decodes a 96-byte array as a Signature
func (s *Signature) FromBytes(b []byte) (*Signature, error) {
	if len(b) != 96 {
		return nil, ErrTPKEDecoding
	}
	pg2, err := bls12381.NewG2().FromCompressed(b)
	if err != nil {
		return nil, err
	}
	s.pg2 = pg2
	return s, nil
}

// SignatureShare is the same as Signature, but used for BLS aggregation
type SignatureShare struct {
	pg2 *bls12381.PointG2
}

// ToBytes encodes a SignatureShare into a byte array whose length is 96
func (s *SignatureShare) ToBytes() []byte {
	return bls12381.NewG2().ToCompressed(s.pg2)
}

// FromBytes decodes a 96-byte array as a SignatureShare
func (s *SignatureShare) FromBytes(b []byte) (*SignatureShare, error) {
	pg2, err := bls12381.NewG2().FromCompressed(b)
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
func AggregateSigShares(matrix [][]int, shares []*SignatureShare, scaler int) *Signature {
	// Be aware of the integer overflow when the size and threshold grow big
	d, coeff := Feldman(matrix)
	d = scaler / d
	// Compute d1
	denominator := big.NewInt(int64(abs(d)))
	g2 := bls12381.NewG2()
	pg2 := g2.Zero()
	// Add up shares with some factors as d1
	for i := 0; i < len(shares); i++ {
		minor := g2.New()
		g2.MulScalar(minor, shares[i].pg2, big.NewInt(int64(abs(coeff[i]))))
		if coeff[i] < 0 {
			g2.Neg(minor, minor)
		}
		g2.Add(pg2, pg2, minor)
	}
	// Divide d1 by d
	g2.MulScalar(pg2, pg2, denominator)
	// Verify
	return NewSignature(pg2)
}
