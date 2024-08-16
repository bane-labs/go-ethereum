package tpke

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

func TestCipherTextEncoding(t *testing.T) {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	ct := &CipherText{
		cMsg:       g1.One(),
		bigR:       g1.One(),
		commitment: g2.One(),
	}
	b := ct.ToBytes()
	result, err := new(CipherText).FromBytes(b)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !g1.Equal(ct.cMsg, result.cMsg) {
		t.Fatalf("cMsg mismatch.")
	}
	if !g1.Equal(ct.bigR, result.bigR) {
		t.Fatalf("bigR mismatch.")
	}
	if !g2.Equal(ct.commitment, result.commitment) {
		t.Fatalf("commitment mismatch.")
	}
}
