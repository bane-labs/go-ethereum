package tpke

import (
	"io"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/rlp"
)

type PrivateKey struct {
	fr *big.Int // A secret number aggregated from DKG sharing
}

var (
	_ rlp.Encoder = &PrivateKey{}
	_ rlp.Decoder = &PrivateKey{}
)

// privateKeyAux is an auxiliary structure for PrivateKey RLP encoding.
type privateKeyAux struct {
	Fr []byte
}

// EncodeRLP implements [rlp.Encoder].
func (priv *PrivateKey) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &privateKeyAux{
		Fr: priv.fr.Bytes(),
	})
}

// DecodeRLP implements [rlp.Decoder].
func (priv *PrivateKey) DecodeRLP(s *rlp.Stream) error {
	aux := new(privateKeyAux)
	if err := s.Decode(aux); err != nil {
		return err
	}
	priv.fr = new(big.Int).SetBytes(aux.Fr)
	return nil
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
