package tpke

import (
	"io"
	"math/big"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/rlp"
)

type Poly struct {
	coeff []*big.Int
}

var (
	_ rlp.Encoder = &Poly{}
	_ rlp.Decoder = &Poly{}
)

// polyAux is an auxiliary structure for Poly RLP encoding.
type polyAux struct {
	Coeff [][]byte
}

// EncodeRLP implements [rlp.Encoder].
func (p *Poly) EncodeRLP(w io.Writer) error {
	coeffs := make([][]byte, len(p.coeff))
	for i := range p.coeff {
		coeffs[i] = p.coeff[i].Bytes()
	}
	return rlp.Encode(w, &polyAux{
		Coeff: coeffs,
	})
}

// DecodeRLP implements [rlp.Decoder].
func (p *Poly) DecodeRLP(s *rlp.Stream) error {
	aux := new(polyAux)
	if err := s.Decode(&aux); err != nil {
		return err
	}
	coeffs := make([]*big.Int, len(aux.Coeff))
	for i := range aux.Coeff {
		coeffs[i] = new(big.Int).SetBytes(aux.Coeff[i])
	}
	p.coeff = coeffs
	return nil
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

var (
	_ rlp.Encoder = &Commitment{}
	_ rlp.Decoder = &Commitment{}
)

// commitmentAux is an auxiliary structure for Commitment RLP marshalling.
type commitmentAux struct {
	Coeff [][bls12381.SizeOfG1AffineCompressed]byte
}

// EncodeRLP implements [rlp.Encoder].
func (c *Commitment) EncodeRLP(w io.Writer) error {
	coeff := make([][bls12381.SizeOfG1AffineCompressed]byte, len(c.coeff))
	for i := range c.coeff {
		coeff[i] = c.coeff[i].Bytes()
	}
	return rlp.Encode(w, &commitmentAux{
		Coeff: coeff,
	})
}

// DecodeRLP implements [rlp.Decoder].
func (c *Commitment) DecodeRLP(s *rlp.Stream) error {
	aux := new(commitmentAux)
	if err := s.Decode(&aux); err != nil {
		return err
	}
	c.coeff = make([]*bls12381.G1Affine, len(aux.Coeff))
	for i := range aux.Coeff {
		c.coeff[i] = new(bls12381.G1Affine)
		_, err := c.coeff[i].SetBytes(aux.Coeff[i][:])
		if err != nil {
			return err
		}
	}

	return nil
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

func (c *Commitment) ToBytes() []byte {
	arr := make([]byte, 0)
	for i := range c.coeff {
		b := encodePointG1(c.coeff[i])
		arr = append(arr, b[:]...)
	}
	return arr
}

func (c *Commitment) FromBytes(b []byte, t int) (*Commitment, error) {
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
