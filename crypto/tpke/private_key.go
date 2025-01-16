package tpke

import (
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
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

// Copy returns a deep copy of PrivateKey
func (sk *PrivateKey) Copy() *PrivateKey {
	return &PrivateKey{
		fr: new(big.Int).Set(sk.fr),
	}
}

func (sk *PrivateKey) Bytes() []byte {
	fe := new(fr.Element).SetBigInt(sk.fr)
	b := fe.Bytes()
	return b[:]
}

func (sk *PrivateKey) FromBytes(b []byte) (*PrivateKey, error) {
	if len(b) != 32 {
		return nil, ErrTPKEScalarDecoding
	}
	fe := new(fr.Element).SetBytes(b)
	sk.fr = fe.BigInt(new(big.Int))
	return sk, nil
}

// GetPublicKey returns a tpke public key for threshold signature
func (sk *PrivateKey) GetPublicKey() *PublicKey {
	return &PublicKey{
		pg1: new(bls12381.G1Affine).ScalarMultiplicationBase(sk.fr),
	}
}

// DecryptShare returns a decryption share for input ciphertext
func (sk *PrivateKey) DecryptShare(ct *CipherText) *DecryptionShare {
	// S=R1*sk
	return &DecryptionShare{
		pg1: new(bls12381.G1Affine).ScalarMultiplication(ct.bigR, sk.fr),
	}
}

// SignShare returns a signature share for input message
func (sk *PrivateKey) SignShare(msg []byte) *SignatureShare {
	// S=H(msg)*sk
	g2Hash, _ := bls12381.HashToG2(msg, Domain)
	sig := new(bls12381.G2Affine).ScalarMultiplication(&g2Hash, sk.fr)
	sig.Neg(sig)
	return &SignatureShare{
		pg2: sig,
	}
}
