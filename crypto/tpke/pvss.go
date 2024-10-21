package tpke

import (
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

type PVSS struct {
	commitment *Commitment          // The commitment of local secret polynomial
	r1         *bls12381.G1Affine   // The commitment of of random r
	r2         *bls12381.G2Affine   // The verifiable commitment of of r1
	bigf       []*bls12381.G1Affine // The commitment of secret sharing
}

// GenerateSecretShares takes a random r to generate PVSS
func GenerateSecretShares(r *big.Int, size int, secret *Secret) (*PVSS, []*big.Int) {
	_, _, _, g2 := bls12381.Generators()
	r1 := new(bls12381.G1Affine).ScalarMultiplicationBase(r)
	r2 := new(bls12381.G2Affine).ScalarMultiplication(&g2, r)
	r2.Neg(r2)
	f := make([]*big.Int, size)
	bigf := make([]*bls12381.G1Affine, size)
	for i := 0; i < size; i++ {
		// Start from 1
		fr := big.NewInt(int64(i + 1))
		// Compute secret share f(i)
		f[i] = secret.poly.evaluate(fr)
		// Compute public share F(i)=f(i)*G1
		bigf[i] = secret.poly.commitment().evaluate(fr)
	}
	return &PVSS{
		commitment: secret.Commitment(),
		r1:         r1,
		r2:         r2,
		bigf:       bigf,
	}, f
}

func (pvss *PVSS) GetCommitment() *Commitment { return pvss.commitment }

func (pvss *PVSS) Encode() []byte {
	arr := make([]byte, 0)
	arr = append(arr, pvss.commitment.Encode()...)
	arr = append(arr, encodePointG1(pvss.r1)...)
	arr = append(arr, encodePointG2(pvss.r2)...)
	for i := 0; i < len(pvss.bigf); i++ {
		arr = append(arr, encodePointG1(pvss.bigf[i])...)
	}
	return arr
}

func (pvss *PVSS) Decode(b []byte, n int, t int) (*PVSS, error) {
	if len(b) != (t+n+1)*128+256 {
		return nil, ErrTPKEDecoding
	}
	comm, err := new(Commitment).Decode(b[:t*128], t)
	if err != nil {
		return nil, err
	}
	r1, err := decodePointG1(b[t*128 : (t+1)*128])
	if err != nil {
		return nil, err
	}
	r2, err := decodePointG2(b[(t+1)*128 : (t+1)*128+256])
	if err != nil {
		return nil, err
	}
	bigf := make([]*bls12381.G1Affine, n)
	for i := 0; i < n; i++ {
		pg1, err := decodePointG1(b[(t+1)*128+256+i*128 : (t+1)*128+256+(i+1)*128])
		if err != nil {
			return nil, err
		}
		bigf[i] = pg1
	}
	pvss.commitment = comm
	pvss.r1 = r1
	pvss.r2 = r2
	pvss.bigf = bigf
	return pvss, nil
}

// VerifyCommitment verifies a PVSS based on its commitment
func (pvss *PVSS) VerifyCommitment() bool {
	_, _, g1, g2 := bls12381.Generators()
	// Verify e(R1,G2)==e(G1,-R2)
	r, err := bls12381.PairingCheck([]bls12381.G1Affine{*pvss.r1, g1}, []bls12381.G2Affine{g2, *pvss.r2})
	if err != nil || !r {
		return false
	}
	for i := 0; i < len(pvss.bigf); i++ {
		fr := big.NewInt(int64(i + 1))
		// Verify F(i)==sum(A_{t-1}*i^(t-1))
		if !pvss.bigf[i].Equal(pvss.commitment.evaluate(fr)) {
			return false
		}

	}
	return true
}

// VerifySecret verifies a PVSS based on shared secret
func (pvss *PVSS) VerifySecret(index int, fi *big.Int) bool {
	_, _, _, g2 := bls12381.Generators()
	// e(r1*fi,g2)=e(bigfi,-r2)
	r, err := bls12381.PairingCheck([]bls12381.G1Affine{*new(bls12381.G1Affine).ScalarMultiplication(pvss.r1, fi), *pvss.bigf[index]}, []bls12381.G2Affine{g2, *pvss.r2})
	if err != nil || !r {
		return false
	} else {
		return true
	}
}

// VerifyRenovate verifies if a PVSS renovate correctly for resharing
func (pvss *PVSS) VerifyRenovate(op *PVSS) bool {
	// Verify the new pvss bigf has the same A0
	if len(pvss.commitment.coeff) != len(op.commitment.coeff) {
		return false
	}
	return pvss.commitment.coeff[0].Equal(op.commitment.coeff[0])
}
