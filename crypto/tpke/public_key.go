package tpke

import (
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

type PublicKey struct {
	pg1 *bls12381.PointG1 // A public value for tpke encryption
}

// NewGlobalPublicKey aggregates and returns a PublicKey
func NewGlobalPublicKey(cs []*Commitment, scaler int) *PublicKey {
	g1 := bls12381.NewG1()
	pg1 := g1.New().Set(cs[0].coeff[0])
	// Add up A0
	for i := 1; i < len(cs); i++ {
		g1.Add(pg1, pg1, cs[i].coeff[0])
	}
	g1.MulScalar(pg1, pg1, big.NewInt(int64(scaler)))
	return &PublicKey{
		pg1: pg1,
	}
}

// Equal compares if two public keys are the same
func (pk *PublicKey) Equal(opk *PublicKey) bool {
	g1 := bls12381.NewG1()
	return g1.Equal(pk.pg1, opk.pg1)
}

// Encrypt returns an encrypted point with encryption commitment
func (pk *PublicKey) Encrypt(msg *bls12381.PointG1) *CipherText {
	r := randScalar()

	// C=M+rpk, R1=rG1, R2=-rG2
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	bigR1 := g1.New()
	bigR2 := g2.New()
	g1.MulScalar(bigR1, g1.One(), r)
	g2.MulScalar(bigR2, g2.One(), r)
	g2.Neg(bigR2, bigR2)

	rpk := g1.New()
	cMsg := g1.New()
	g1.MulScalar(rpk, pk.pg1, r)
	g1.Add(cMsg, msg, rpk)

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
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	g2Hash, _ := g2.HashToCurve(msg, Domain)

	// e(pk,g2Hash)=e(g1,-sig)
	pairing := bls12381.NewPairingEngine()
	pairing.AddPair(pk.pg1, g2Hash)
	pairing.AddPair(g1.One(), sig.pg2)
	return pairing.Check()
}
