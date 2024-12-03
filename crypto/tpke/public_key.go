package tpke

import (
	"fmt"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

// PublicKeyLen is the length of PublicKey in serialized compressed representation.
const PublicKeyLen = bls12381.SizeOfG1AffineCompressed

type PublicKey struct {
	pg1 *bls12381.G1Affine // A public value for tpke encryption
}

// NewGlobalPublicKey aggregates and returns a PublicKey
func NewGlobalPublicKey(cs []*Commitment, scaler int) *PublicKey {
	pg1 := new(bls12381.G1Affine).Set(cs[0].coeff[0])
	// Add up A0
	for i := 1; i < len(cs); i++ {
		pg1.Add(pg1, cs[i].coeff[0])
	}
	pg1.ScalarMultiplication(pg1, big.NewInt(int64(scaler)))
	return &PublicKey{
		pg1: pg1,
	}
}

// Bytes serializes PublicKey into byte slice using compressed [bls12381.G1Affine]
// representation.
func (pk *PublicKey) Bytes() []byte {
	res := pk.pg1.Bytes()
	return res[:]
}

// FromBytes deserializes PublicKey from the given byte slice. It expects compressed
// PublicKey representation as an input (see [PublicKey.Bytes] documentation).
func (pk *PublicKey) FromBytes(bytes []byte) error {
	if len(bytes) != PublicKeyLen {
		return fmt.Errorf("invalid public key length: expected %d, got %d", PublicKeyLen, len(bytes))
	}
	pk.pg1 = new(bls12381.G1Affine)
	_, err := pk.pg1.SetBytes(bytes)
	return err
}

func (pk *PublicKey) Encode() []byte {
	return encodePointG1(pk.pg1)
}

func (pk *PublicKey) Decode(b []byte) (*PublicKey, error) {
	pg1, err := decodePointG1(b)
	if err != nil {
		return nil, err
	}
	pk.pg1 = pg1
	return pk, nil
}

// Equal compares if two public keys are the same
func (pk *PublicKey) Equal(opk *PublicKey) bool {
	return pk.pg1.Equal(opk.pg1)
}

// Encrypt returns an encrypted point with encryption commitment
func (pk *PublicKey) Encrypt(msg *bls12381.G1Affine) *CipherText {
	r := randScalar()

	// C=M+rpk, R1=rG1, R2=-rG2
	_, _, _, g2 := bls12381.Generators()
	bigR1 := new(bls12381.G1Affine).ScalarMultiplicationBase(r)
	bigR2 := new(bls12381.G2Affine).ScalarMultiplication(&g2, r)
	bigR2.Neg(bigR2)

	rpk := new(bls12381.G1Affine).ScalarMultiplication(pk.pg1, r)
	cMsg := new(bls12381.G1Affine).Add(msg, rpk)

	return &CipherText{
		cMsg:       cMsg,
		bigR:       bigR1,
		commitment: bigR2,
	}
}

// VerifySigShare verifies a signature in form of a single signature
func (pk *PublicKey) VerifySigShare(msg []byte, sig *SignatureShare) bool {
	return pk.VerifySig(msg, (*Signature)(sig))
}

// VerifySig verifies a signature with corresponding message
func (pk *PublicKey) VerifySig(msg []byte, sig *Signature) bool {
	g2Hash, _ := bls12381.HashToG2(msg, Domain)

	return pk.Verify(&g2Hash, sig) == nil
}

// Verify verifies provided signature against the corresponding message hash.
func (pk *PublicKey) Verify(hash *bls12381.G2Affine, sig *Signature) error {
	_, _, g1, _ := bls12381.Generators()

	// e(pk,g2Hash)=e(g1,-sig)
	ok, err := bls12381.PairingCheck([]bls12381.G1Affine{*pk.pg1, g1}, []bls12381.G2Affine{*hash, *sig.pg2})
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
