package tpke

import (
	"math/big"
	"testing"
)

func TestPoly_AddAssign(t *testing.T) {
	fr1 := big.NewInt(1)
	poly := &Poly{
		coeff: []*big.Int{
			fr1,
			fr1,
			fr1,
		},
	}
	poly2 := &Poly{
		coeff: []*big.Int{
			fr1,
			fr1,
			fr1,
			fr1,
			fr1,
		},
	}
	t.Logf("%v", poly.coeff)
	poly.AddAssign(poly2)
	t.Logf("%v", poly.coeff)
}

func TestPoly_evaluate(t *testing.T) {
	expectedFr := big.NewInt(47)
	fr1 := big.NewInt(2)
	fr2 := big.NewInt(3)
	fr3 := big.NewInt(4)
	poly := &Poly{
		[]*big.Int{
			fr1,
			fr2,
			fr3,
		},
	}

	result := poly.evaluate(big.NewInt(3))
	if result.Cmp(expectedFr) != 0 {
		t.Errorf("results are not equal.")
	}
	// PASS
}

func TestPoly_commitment(t *testing.T) {
	fr1 := big.NewInt(2)
	fr2 := big.NewInt(3)
	fr3 := big.NewInt(4)

	poly := &Poly{
		coeff: []*big.Int{
			fr1,
			fr2,
			fr3,
		},
	}

	com := poly.commitment()
	result := com.evaluate(big.NewInt(3))
	t.Logf("%v", com)
	t.Logf("%v", result)
}
