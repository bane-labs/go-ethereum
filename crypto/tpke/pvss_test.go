package tpke

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

func TestPVSSEncoding(t *testing.T) {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	poly := randomPoly(2)
	comm := poly.commitment()
	bigf := make([]*bls12381.PointG1, 3)
	bigf[0] = comm.evaluate(big.NewInt(1))
	bigf[1] = comm.evaluate(big.NewInt(2))
	bigf[2] = comm.evaluate(big.NewInt(3))
	pvss := &PVSS{
		commitment: poly.commitment(),
		r1:         g1.One(),
		r2:         g2.One(),
		bigf:       bigf,
	}
	b := pvss.ToBytes()
	result, err := new(PVSS).FromBytes(b, 3, 2)
	if err != nil {
		t.Fatalf(err.Error())
	}
	for i, pg1 := range pvss.commitment.coeff {
		if !g1.Equal(pg1, result.commitment.coeff[i]) {
			t.Fatalf("commitment mismatch.")
		}
	}
	if !g1.Equal(pvss.r1, result.r1) {
		t.Fatalf("r1 mismatch.")
	}
	if !g2.Equal(pvss.r2, result.r2) {
		t.Fatalf("r2 mismatch.")
	}
	for i, pg1 := range pvss.bigf {
		if !g1.Equal(pg1, result.bigf[i]) {
			t.Fatalf("bigf mismatch.")
		}
	}
}
