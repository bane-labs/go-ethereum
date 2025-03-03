package tpke

import (
	"errors"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"
)

const (
	// FPSize is the number of bytes needed to represent a field element in
	// Montgomery form.
	FPSize = fp.Bytes
	// CipherTextSize is the number of bytes needed to represent CipherText
	// in serialized representation.
	CipherTextSize = 4 * FPSize
	// DecryptionShareSize is the size of a single decryption message.
	DecryptionShareSize = FPSize
)

var (
	workerCount           = 24
	ErrTPKECiphertext     = errors.New("crypto/tpke: invalid tpke ciphertext")
	ErrTPKEDecryption     = errors.New("crypto/tpke: tpke decryption failed")
	ErrTPKEDecoding       = errors.New("crypto/tpke: invalid decode length")
	ErrTPKELengthMismatch = errors.New("crypto/tpke: input length mismatch")
)

// The encrypted message carrier in TPKE
type CipherText struct {
	cMsg       *bls12381.G1Affine // Encrypted message
	bigR       *bls12381.G1Affine // Verifiable commitment of encryption commitment
	commitment *bls12381.G2Affine // Encryption commitment
}

// ToBytes encodes a CipherText into a byte array whose length is 192
func (ct *CipherText) ToBytes() []byte {
	out := make([]byte, CipherTextSize)
	bmsg := ct.cMsg.Bytes()
	br := ct.bigR.Bytes()
	bc := ct.commitment.Bytes()
	copy(out[:FPSize], bmsg[:])
	copy(out[FPSize:2*FPSize], br[:])
	copy(out[2*FPSize:4*FPSize], bc[:])
	return out
}

// FromBytes reads the first 192 bytes of input array and decodes as CipherText
func (ct *CipherText) FromBytes(b []byte) (*CipherText, error) {
	if len(b) != CipherTextSize {
		return nil, ErrTPKEDecoding
	}
	cMsg := new(bls12381.G1Affine)
	bigR := new(bls12381.G1Affine)
	commitment := new(bls12381.G2Affine)
	_, err := cMsg.SetBytes(b[:FPSize])
	if err != nil {
		return nil, err
	}
	_, err = bigR.SetBytes(b[FPSize : 2*FPSize])
	if err != nil {
		return nil, err
	}
	_, err = commitment.SetBytes(b[2*FPSize : 4*FPSize])
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
	_, _, g1, g2 := bls12381.Generators()
	r, err := bls12381.PairingCheck([]bls12381.G1Affine{*ct.bigR, g1}, []bls12381.G2Affine{g2, *ct.commitment})
	if err != nil || !r {
		return ErrTPKECiphertext
	}
	return nil
}

// The decryption message for further aggregation
type DecryptionShare struct {
	pg1 *bls12381.G1Affine
}

// ToBytes encodes a DecryptionShare into a byte array whose length is 48
func (s *DecryptionShare) ToBytes() []byte {
	b := s.pg1.Bytes()
	return b[:]
}

// FromBytes decodes a 48-byte array as a DecryptionShare
func (s *DecryptionShare) FromBytes(b []byte) (*DecryptionShare, error) {
	pg1 := new(bls12381.G1Affine)
	_, err := pg1.SetBytes(b)
	if err != nil {
		return nil, err
	}
	s.pg1 = pg1
	return s, nil
}

// The task message for local decryption and verification
type workerTask struct {
	i     int
	ct    *CipherText
	pk    *PublicKey
	ss    []*DecryptionShare
	coeff []int
	d     *big.Int
}

// The task result about local decryption and verification
type workerResult struct {
	i   int
	msg *bls12381.G1Affine
	err error
}

// AggregateAndDecrypt tries to aggregate DecryptionShares and decrypts CipherTexts with verification
// This method takes a batch of ordered CipherTexts, DecryptionShares and a matrix for Vandermonde
// The size of DecryptionShare array should be len(message)*len(ciphertext)
// Each row of the input matrix should be [1, i, i^2, ..., i^(threshold-1)], i is the dkg key index
// The message amount should be larger than threshold, otherwise the result will be wrong
func AggregateAndDecrypt(cts []*CipherText, matrix [][]int, shares [][]*DecryptionShare, pub *PublicKey, scaler int) ([]*bls12381.G1Affine, error) {
	for i := 0; i < len(shares); i++ {
		if len(cts) != len(shares[i]) {
			return nil, ErrTPKELengthMismatch
		}
	}
	if len(matrix) != len(shares) {
		return nil, ErrTPKELengthMismatch
	}

	// Be aware of the integer overflow when the size and threshold of tpke grow big
	d, coeff := Vandermonde(matrix)
	d = scaler / d
	results := make([]*bls12381.G1Affine, len(cts))
	// Compute M=C-d1/d
	denominator := big.NewInt(int64(abs(d)))
	if d < 0 {
		denominator.Neg(denominator)
	}
	in := make(chan workerTask, len(cts))
	out := make(chan workerResult, len(cts))
	for i := 0; i < workerCount; i++ {
		go startWorker(in, out)
	}
	for i := 0; i < len(cts); i++ {
		ss := make([]*DecryptionShare, len(shares))
		for j := 0; j < len(shares); j++ {
			ss[j] = shares[j][i]
		}
		// Parallel tasks
		in <- workerTask{i, cts[i], pub, ss, coeff, denominator}
	}
	close(in)
	// Verification passes if the decrypted rpk contains the same r as the ciphertext declares
	// If a user (the encryptor) use a different r to generate cMsg, no error will be detected
	// here, but the following aes decryption will fail
	for i := 0; i < len(cts); i++ {
		r := <-out
		if r.err != nil {
			return nil, r.err
		}
		results[r.i] = r.msg
	}

	return results, nil
}

// startVerifier verifies if the decrypted rpk is the same as the message declares
func startWorker(in <-chan workerTask, out chan<- workerResult) {
	for {
		t, ok := <-in
		if !ok {
			return
		}
		// Try Decrypt
		rpk := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
		// Add up shares with some factors as d1, and plus -1
		for i := 0; i < len(t.ss); i++ {
			minor := new(bls12381.G1Affine).ScalarMultiplication(t.ss[i].pg1, big.NewInt(int64(abs(t.coeff[i]))))
			if t.coeff[i] < 0 {
				minor.Neg(minor)
			}
			rpk.Add(rpk, minor)
		}
		// Divide -d1 by d
		rpk.ScalarMultiplication(rpk, t.d)
		msg := new(bls12381.G1Affine).Sub(t.ct.cMsg, rpk)

		// Verify
		_, _, _, g2 := bls12381.Generators()
		// Decrypted rpk is not correct, e(pk,rG2)!=e(rpk,G2), decryption fails
		r, err := bls12381.PairingCheck([]bls12381.G1Affine{*t.pk.pg1, *rpk}, []bls12381.G2Affine{*t.ct.commitment, g2})
		if err != nil || !r {
			out <- workerResult{t.i, nil, ErrTPKEDecryption}
		} else {
			out <- workerResult{t.i, msg, nil}
		}
	}
}
