package tpke

import (
	"io"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/rlp"
)

type PublicKey struct {
	pg1 *bls12381.G1Affine // A public value for tpke encryption
}

var (
	_ rlp.Encoder = &PublicKey{}
	_ rlp.Decoder = &PublicKey{}
)

// publicKeyAux is an auxiliary structure for PublicKey RLP encoding.
type publicKeyAux struct {
	Pg1 [bls12381.SizeOfG1AffineCompressed]byte
}

// EncodeRLP implements [rlp.Encoder].
func (pub *PublicKey) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &publicKeyAux{
		Pg1: pub.pg1.Bytes(),
	})
}

// DecodeRLP implements [rlp.Decoder].
func (pub *PublicKey) DecodeRLP(s *rlp.Stream) error {
	aux := new(publicKeyAux)
	if err := s.Decode(aux); err != nil {
		return err
	}
	var err error
	pub.pg1 = new(bls12381.G1Affine)
	_, err = pub.pg1.SetBytes(aux.Pg1[:])
	return err
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
	_, _, g1, _ := bls12381.Generators()
	g2Hash, _ := bls12381.HashToG2(msg, Domain)

	// e(pk,g2Hash)=e(g1,-sig)
	r, err := bls12381.PairingCheck([]bls12381.G1Affine{*pk.pg1, g1}, []bls12381.G2Affine{g2Hash, *sig.pg2})
	if err != nil || !r {
		return false
	} else {
		return true
	}
}
