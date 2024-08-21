package tpke

import (
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

type PrivateKey struct {
	fr *big.Int // A secret number aggregated from DKG sharing
}

func RandomPrivateKey() *PrivateKey {
	fr := randScalar()
	return &PrivateKey{
		fr: fr,
	}
}

// NewPrivateKey returns a tpke private key for threshold decryption
func NewPrivateKey(secretShares []*big.Int) *PrivateKey {
	fr := new(big.Int).Set(secretShares[0])
	// Add up fi
	for i := 1; i < len(secretShares); i++ {
		fr.Add(fr, secretShares[i])
	}
	return &PrivateKey{
		fr: fr,
	}
}

// GetPublicKey returns a tpke public key for threshold signature
func (sk *PrivateKey) GetPublicKey() *PublicKey {
	g1 := bls12381.NewG1()
	pg1 := g1.New()
	return &PublicKey{
		pg1: g1.MulScalar(pg1, g1.One(), sk.fr),
	}
}

// DecryptShare returns a decryption share for input ciphertext
func (sk *PrivateKey) DecryptShare(ct *CipherText) *DecryptionShare {
	// S=R1*sk
	g1 := bls12381.NewG1()
	pg1 := g1.New().Set(ct.bigR)
	g1.MulScalar(pg1, pg1, sk.fr)
	return &DecryptionShare{
		pg1: pg1,
	}
}

// SignShare returns a signature share for input message
func (sk *PrivateKey) SignShare(msg []byte) *SignatureShare {
	// S=H(msg)*sk
	g2 := bls12381.NewG2()
	g2Hash, _ := g2.HashToCurve(msg, Domain)
	sig := g2.New()
	g2.MulScalar(sig, g2Hash, sk.fr)
	g2.Neg(sig, sig)
	return &SignatureShare{
		pg2: sig,
	}
}
