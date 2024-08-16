package tpke

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

var (
	fpByteSize        = 48
	ErrTPKECiphertext = errors.New("crypto/tpke: invalid tpke ciphertext")
	ErrTPKEDecryption = errors.New("crypto/tpke: tpke decryption failed")
	ErrTPKEDecoding   = errors.New("crypto/tpke: invalid decode length")
)

// The encrypted message carrier in TPKE
type CipherText struct {
	cMsg       *bls12381.PointG1 // Encrypted message
	bigR       *bls12381.PointG1 // Verifiable commitment of encryption commitment
	commitment *bls12381.PointG2 // Encryption commitment
}

// ToBytes encodes a CipherText into a byte array whose length is 192
func (ct *CipherText) ToBytes() []byte {
	out := make([]byte, 4*fpByteSize)
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	copy(out[:fpByteSize], g1.ToCompressed(ct.cMsg))
	copy(out[fpByteSize:2*fpByteSize], g1.ToCompressed(ct.bigR))
	copy(out[2*fpByteSize:4*fpByteSize], g2.ToCompressed(ct.commitment))
	return out
}

// FromBytes reads the first 192 bytes of input array and decodes as CipherText
func (ct *CipherText) FromBytes(b []byte) (*CipherText, error) {
	if len(b) != 192 {
		return nil, ErrTPKEDecoding
	}
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	cMsg, err := g1.FromCompressed(b[:fpByteSize])
	if err != nil {
		return nil, err
	}
	bigR, err := g1.FromCompressed(b[fpByteSize : 2*fpByteSize])
	if err != nil {
		return nil, err
	}
	commitment, err := g2.FromCompressed(b[2*fpByteSize : 4*fpByteSize])
	if err != nil {
		return nil, err
	}
	ct.cMsg = cMsg
	ct.bigR = bigR
	ct.commitment = commitment
	return ct, nil
}

// Verify checks if the CipherText has a valid commitment for random encryption
// If this returns no error, then the random r is confirmed without knowledge
func (ct *CipherText) Verify() error {
	// User sends an invalid commitment for his random r
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	pairing := bls12381.NewPairingEngine()
	pairing.AddPair(ct.bigR, g2.One())
	pairing.AddPair(g1.One(), ct.commitment)
	if !pairing.Check() {
		return ErrTPKECiphertext
	}
	return nil
}

// The decryption message for further aggregation
type DecryptionShare struct {
	pg1 *bls12381.PointG1
}

// ToBytes encodes a DecryptionShare into a byte array whose length is 48
func (s *DecryptionShare) ToBytes() []byte {
	return bls12381.NewG1().ToCompressed(s.pg1)
}

// FromBytes decodes a 48-byte array as a DecryptionShare
func (s *DecryptionShare) FromBytes(b []byte) (*DecryptionShare, error) {
	pg1, err := bls12381.NewG1().FromCompressed(b)
	if err != nil {
		return nil, err
	}
	s.pg1 = pg1
	return s, nil
}

// AggregateAndDecrypt tries to aggregate DecryptionShares and decrypts CipherTexts with verification
// This method takes a batch of ordered CipherTexts, DecryptionShares and a matrix for Feldman
// The size of DecryptionShare array should be len(message)*len(ciphertext)
// Each row of the input matrix should be [1, i, i^2, ..., i^(threshold-1)], i is the dkg key index
// The message amount should be larger than threshold, otherwise the result will be wrong
func AggregateAndDecrypt(cts []*CipherText, matrix [][]int, shares [][]*DecryptionShare, pub *PublicKey, scaler int) ([]*bls12381.PointG1, error) {
	// Be aware of the integer overflow when the size and threshold of tpke grow big
	d, coeff := Feldman(matrix)
	d = scaler / d
	results := make([]*bls12381.PointG1, len(cts))
	// Compute M=C-d1/d
	denominator := big.NewInt(int64(abs(d)))
	if d < 0 {
		denominator.Neg(denominator)
	}
	ch := make(chan error, len(cts))
	g1 := bls12381.NewG1()
	for i := 0; i < len(cts); i++ {
		rpk := g1.Zero()
		// Add up shares with some factors as d1
		for j := 0; j < len(shares); j++ {
			minor := g1.New()
			g1.MulScalar(minor, shares[j][i].pg1, big.NewInt(int64(abs(coeff[j]))))
			if coeff[j] < 0 {
				g1.Neg(minor, minor)
			}
			g1.Add(rpk, rpk, minor)
		}
		// Divide d1 by d
		g1.MulScalar(rpk, rpk, denominator)
		// Decrypt
		results[i] = g1.Sub(g1.Zero(), cts[i].cMsg, rpk)
		// Verify the decryption
		go parallelVerify(i, cts[i], pub.pg1, rpk, ch)
	}
	// TODO: return the index of failed decryption for further usage
	for i := 0; i < len(cts); i++ {
		err := <-ch
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// parallelVerify verifies if the decrypted rpk is the same as the message declares
func parallelVerify(index int, ct *CipherText, pk *bls12381.PointG1, rpk *bls12381.PointG1, ch chan<- error) {
	// User sends an invalid commitment for his random r
	g2 := bls12381.NewG2()
	pairing := bls12381.NewPairingEngine()
	// Decrypted rpk is not correct, e(pk,rG2)!=e(rpk,G2), decryption fails
	pairing.AddPair(pk, ct.commitment)
	pairing.AddPair(rpk, g2.One())
	if !pairing.Check() {
		ch <- ErrTPKEDecryption
		return
	}
	ch <- nil
}
