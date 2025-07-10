package tpke

import (
	"math/big"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/stretchr/testify/require"
)

func TestCipherTextEncoding(t *testing.T) {
	_, _, _, g2 := bls12381.Generators()
	ct := &CipherText{
		cMsg:       new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(1)),
		bigR:       new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(1)),
		commitment: new(bls12381.G2Affine).ScalarMultiplication(&g2, big.NewInt(1)),
	}
	b := ct.ToBytes()
	result, err := new(CipherText).FromBytes(b)
	require.NoError(t, err)
	if !ct.cMsg.Equal(result.cMsg) {
		t.Fatalf("cMsg mismatch.")
	}
	if !ct.bigR.Equal(result.bigR) {
		t.Fatalf("bigR mismatch.")
	}
	if !ct.commitment.Equal(result.commitment) {
		t.Fatalf("commitment mismatch.")
	}
}
