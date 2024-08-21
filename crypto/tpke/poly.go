package tpke

import (
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

type Poly struct {
	coeff []*big.Int
}

func randomPoly(degree int) *Poly {
	coeff := make([]*big.Int, degree)

	for i := range coeff {
		fr := randScalar()
		coeff[i] = fr
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
	g1 := bls12381.NewG1()
	ci := g1.New()
	coeff := make([]*bls12381.PointG1, len(p.coeff))
	for i := range coeff {
		g1.MulScalar(ci, g1.One(), p.coeff[i])
		coeff[i] = g1.New().Set(ci)
	}
	return &Commitment{
		coeff: coeff,
	}
}

type Commitment struct {
	coeff []*bls12381.PointG1
}

func (c *Commitment) Clone() *Commitment {
	g1 := bls12381.NewG1()
	coeff := make([]*bls12381.PointG1, len(c.coeff))
	for i := range coeff {
		coeff[i] = g1.New().Set(c.coeff[i])
	}
	return &Commitment{
		coeff: coeff,
	}
}

func (c *Commitment) ToBytes() []byte {
	g1 := bls12381.NewG1()
	arr := make([]byte, 0)
	for i := range c.coeff {
		arr = append(arr, g1.EncodePoint(c.coeff[i])...)
	}
	return arr
}

func (c *Commitment) FromBytes(b []byte, t int) (*Commitment, error) {
	if len(b) != t*128 {
		return nil, ErrTPKEDecoding
	}
	g1 := bls12381.NewG1()
	arr := make([]*bls12381.PointG1, t)
	for i := 0; i < t; i++ {
		pg1, err := g1.DecodePoint(b[i*128 : (i+1)*128])
		if err != nil {
			return nil, err
		}
		arr[i] = pg1
	}
	c.coeff = arr
	return c, nil
}

func (c *Commitment) evaluate(x *big.Int) *bls12381.PointG1 {
	g1 := bls12381.NewG1()
	if len(c.coeff) == 0 {
		return g1.Zero()
	}
	i := len(c.coeff) - 1
	result := g1.New().Set(c.coeff[i])
	for i >= 0 {
		if i != len(c.coeff)-1 {
			g1.MulScalar(result, result, x)
			g1.Add(result, result, c.coeff[i])
		}
		i--
	}
	return result
}

func (c *Commitment) AddAssign(op *Commitment) {
	g1 := bls12381.NewG1()
	pLen := len(c.coeff)
	opLen := len(op.coeff)
	for pLen < opLen {
		c.coeff = append(c.coeff, g1.New().Zero())
		pLen++
	}
	for i := range c.coeff {
		g1.Add(c.coeff[i], c.coeff[i], op.coeff[i])
	}
}

func (c *Commitment) Equals(oc *Commitment) bool {
	if len(c.coeff) != len(oc.coeff) {
		return false
	}
	g1 := bls12381.NewG1()
	for i := range c.coeff {
		if !g1.Equal(c.coeff[i], oc.coeff[i]) {
			return false
		}
	}
	return true
}
