package tpke

import (
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

type Poly struct {
	coeff []*big.Int
}

func randomPoly(degree int) (*Poly, error) {
	coeff := make([]*big.Int, degree)

	for i := range coeff {
		fr, err := randScalar()
		if err != nil {
			return nil, err
		}
		coeff[i] = fr
	}
	return &Poly{
		coeff: coeff,
	}, nil
}

func predictablePoly(degree int, secret []byte, public []byte, round int) *Poly {
	coeff := make([]*big.Int, degree)
	for i := range coeff {
		coeff[i] = predictableRandScalar(secret, public, byte(round), byte(i))
	}
	return &Poly{
		coeff: coeff,
	}
}

func (p *Poly) evaluate(x *big.Int) *big.Int {
	i := len(p.coeff) - 1
	result := new(big.Int).Set(p.coeff[i])
	for i >= 0 {
		if i != len(p.coeff)-1 {
			result.Mul(result, x)
			result.Add(result, p.coeff[i])
		}
		i--
	}
	return result
}

// Copy returns a deep copy of Poly
func (p *Poly) Copy() *Poly {
	coeff := make([]*big.Int, len(p.coeff))
	for i := range coeff {
		coeff[i] = new(big.Int).Set(p.coeff[i])
	}
	return &Poly{
		coeff: coeff,
	}
}

func (p *Poly) AddAssign(op *Poly) {
	pLen := len(p.coeff)
	opLen := len(op.coeff)
	for pLen < opLen {
		p.coeff = append(p.coeff, big.NewInt(0))
		pLen++
	}
	for i := range p.coeff {
		p.coeff[i].Add(p.coeff[i], op.coeff[i])
	}
}

func (p *Poly) MulAssign(x *big.Int) {
	// TODO : check if op is zero
	for _, c := range p.coeff {
		c.Mul(c, x)
	}
}

func (p *Poly) commitment() *Commitment {
	coeff := make([]*bls12381.G1Affine, len(p.coeff))
	for i := range coeff {
		coeff[i] = new(bls12381.G1Affine).ScalarMultiplicationBase(p.coeff[i])
	}
	return &Commitment{
		coeff: coeff,
	}
}

type Commitment struct {
	coeff []*bls12381.G1Affine
}

func (c *Commitment) Clone() *Commitment {
	coeff := make([]*bls12381.G1Affine, len(c.coeff))
	for i := range coeff {
		coeff[i] = new(bls12381.G1Affine).Set(c.coeff[i])
	}
	return &Commitment{
		coeff: coeff,
	}
}

func (c *Commitment) Encode() []byte {
	arr := make([]byte, 0)
	for i := range c.coeff {
		b := encodePointG1(c.coeff[i])
		arr = append(arr, b[:]...)
	}
	return arr
}

func (c *Commitment) Decode(b []byte, t int) (*Commitment, error) {
	if len(b) != t*128 {
		return nil, ErrTPKEDecoding
	}
	arr := make([]*bls12381.G1Affine, t)
	for i := 0; i < t; i++ {
		pg1, err := decodePointG1(b[i*128 : (i+1)*128])
		if err != nil {
			return nil, err
		}
		arr[i] = pg1
	}
	c.coeff = arr
	return c, nil
}

func (c *Commitment) evaluate(x *big.Int) *bls12381.G1Affine {
	if len(c.coeff) == 0 {
		return new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	}
	i := len(c.coeff) - 1
	result := new(bls12381.G1Affine).Set(c.coeff[i])
	for i >= 0 {
		if i != len(c.coeff)-1 {
			result.ScalarMultiplication(result, x)
			result.Add(result, c.coeff[i])
		}
		i--
	}
	return result
}

func (c *Commitment) AddAssign(op *Commitment) {
	pLen := len(c.coeff)
	opLen := len(op.coeff)
	for pLen < opLen {
		c.coeff = append(c.coeff, new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0)))
		pLen++
	}
	for i := range c.coeff {
		c.coeff[i].Add(c.coeff[i], op.coeff[i])
	}
}

func (c *Commitment) Equals(oc *Commitment) bool {
	if len(c.coeff) != len(oc.coeff) {
		return false
	}
	for i := range c.coeff {
		if !c.coeff[i].Equal(oc.coeff[i]) {
			return false
		}
	}
	return true
}
