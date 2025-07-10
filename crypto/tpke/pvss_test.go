package tpke

import (
	"math/big"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/stretchr/testify/require"
)

func TestPVSSEncoding(t *testing.T) {
	_, _, g1, g2 := bls12381.Generators()
	poly, err := randomPoly(2)
	require.NoError(t, err)
	comm := poly.commitment()
	bigf := make([]*bls12381.G1Affine, 3)
	bigf[0] = comm.evaluate(big.NewInt(1))
	bigf[1] = comm.evaluate(big.NewInt(2))
	bigf[2] = comm.evaluate(big.NewInt(3))
	pvss := &PVSS{
		commitment: poly.commitment(),
		r1:         &g1,
		r2:         &g2,
		bigf:       bigf,
	}
	b := pvss.Encode()
	result, err := new(PVSS).Decode(b, 3, 2)
	require.NoError(t, err)
	for i, pg1 := range pvss.commitment.coeff {
		if !pg1.Equal(result.commitment.coeff[i]) {
			t.Fatalf("commitment mismatch.")
		}
	}
	if !pvss.r1.Equal(result.r1) {
		t.Fatalf("r1 mismatch.")
	}
	if !pvss.r2.Equal(result.r2) {
		t.Fatalf("r2 mismatch.")
	}
	for i, pg1 := range pvss.bigf {
		if !pg1.Equal(result.bigf[i]) {
			t.Fatalf("bigf mismatch.")
		}
	}
}
