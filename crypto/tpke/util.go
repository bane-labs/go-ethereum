package tpke

import (
	"bytes"
	"crypto/rand"
	"errors"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"
)

var (
	ErrTPKEG1Decoding           = errors.New("crypto/tpke: invalid g1 decoding")
	ErrTPKEG2Decoding           = errors.New("crypto/tpke: invalid g2 decoding")
	ErrTPKEFieldElementDecoding = errors.New("crypto/tpke: invalid fe decoding")
	ErrTPKEScalarDecoding       = errors.New("crypto/tpke: invalid scalar decoding")
)

// randScalar returns a random big int
func randScalar() (*big.Int, error) {
	var k *big.Int
	var err error
	for {
		k, err = rand.Int(rand.Reader, ecc.BLS12_381.ScalarField())
		if err != nil {
			return nil, err
		}
		if k.Sign() > 0 {
			break
		}
	}
	return k, nil
}

// randPG1 returns a random bls12381 g1 point
func randPG1() (*bls12381.G1Affine, error) {
	r, err := randScalar()
	if err != nil {
		return nil, err
	}
	return new(bls12381.G1Affine).ScalarMultiplicationBase(r), nil
}

func decodePointG1(in []byte) (*bls12381.G1Affine, error) {
	if len(in) != 128 {
		return nil, ErrTPKEG1Decoding
	}
	// decode x
	x, err := decodeBLS12381FieldElement(in[:64])
	if err != nil {
		return nil, err
	}
	// decode y
	y, err := decodeBLS12381FieldElement(in[64:])
	if err != nil {
		return nil, err
	}
	elem := bls12381.G1Affine{X: x, Y: y}
	if !elem.IsOnCurve() || !elem.IsInSubGroup() {
		return nil, ErrTPKEG1Decoding
	}

	return &elem, nil
}

// decodePointG2 given encoded (x, y) coordinates in 256 bytes returns a valid G2 Point.
func decodePointG2(in []byte) (*bls12381.G2Affine, error) {
	if len(in) != 256 {
		return nil, ErrTPKEG2Decoding
	}
	x0, err := decodeBLS12381FieldElement(in[:64])
	if err != nil {
		return nil, err
	}
	x1, err := decodeBLS12381FieldElement(in[64:128])
	if err != nil {
		return nil, err
	}
	y0, err := decodeBLS12381FieldElement(in[128:192])
	if err != nil {
		return nil, err
	}
	y1, err := decodeBLS12381FieldElement(in[192:])
	if err != nil {
		return nil, err
	}

	p := bls12381.G2Affine{X: bls12381.E2{A0: x0, A1: x1}, Y: bls12381.E2{A0: y0, A1: y1}}
	if !p.IsOnCurve() || !p.IsInSubGroup() {
		return nil, ErrTPKEG2Decoding
	}
	return &p, err
}

// decodeBLS12381FieldElement decodes BLS12-381 elliptic curve field element.
// Removes top 16 bytes of 64 byte input.
func decodeBLS12381FieldElement(in []byte) (fp.Element, error) {
	if len(in) != 64 {
		return fp.Element{}, ErrTPKEFieldElementDecoding
	}
	// check top bytes
	for i := 0; i < 16; i++ {
		if in[i] != byte(0x00) {
			return fp.Element{}, ErrTPKEFieldElementDecoding
		}
	}
	var res [48]byte
	copy(res[:], in[16:])

	return fp.BigEndian.Element(&res)
}

// encodePointG1 encodes a point into 128 bytes.
func encodePointG1(p *bls12381.G1Affine) []byte {
	out := make([]byte, 128)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[16:]), p.X)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[64+16:]), p.Y)
	return out
}

// encodePointG2 encodes a point into 256 bytes.
func encodePointG2(p *bls12381.G2Affine) []byte {
	out := make([]byte, 256)
	// encode x
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[16:16+48]), p.X.A0)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[80:80+48]), p.X.A1)
	// encode y
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[144:144+48]), p.Y.A0)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[208:208+48]), p.Y.A1)
	return out
}

func polyRecover(xs []int, ys []*big.Int) []*big.Int {
	if len(xs) != len(ys) {
		panic("array length mismatch")
	}
	// Compute lagrange
	length := len(ys)
	ns := make([][]int, length)
	ds := make([]int, length)
	for i := 0; i < length; i++ {
		ns[i], ds[i] = lagrange(xs, i)
	}
	bigR := 1
	for i := 0; i < length; i++ {
		bigR *= ds[i]
	}
	for i := 0; i < length; i++ {
		div := bigR / ds[i]
		for j := 0; j < len(ns[i]); j++ {
			ns[i][j] *= div
		}
		ds[i] = bigR
	}

	// Recover polynomial
	t := make([]*big.Int, 0)
	for i := 0; i < length; i++ {
		poly := make([]*big.Int, len(ns[i]))
		for j := 0; j < len(ns[i]); j++ {
			poly[j] = new(big.Int).Mul(big.NewInt(int64(ns[i][j])), ys[i])
		}
		t = polyAdd(t, poly)
	}
	for i := 0; i < len(t); i++ {
		t[i] = new(big.Int).Div(t[i], big.NewInt(int64(bigR)))
	}
	return t
}

func lagrange(xs []int, n int) ([]int, int) {
	numerator := []int{1}
	for i := 0; i < len(xs); i++ {
		if n == i {
			continue
		}
		numerator = polyMul(numerator, []int{-xs[i], 1})
	}
	denominator := 1
	for i := 0; i < len(xs); i++ {
		if n == i {
			continue
		}
		denominator *= xs[n] - xs[i]
	}
	return numerator, denominator
}

// (a0+a1x+a2x^2)*(b0+b1x+b2x^2)
func polyMul(p1 []int, p2 []int) []int {
	r := make([]int, len(p1)+len(p2)-1)
	for i := 0; i < len(p1); i++ {
		for j := 0; j < len(p2); j++ {
			r[i+j] += p1[i] * p2[j]
		}
	}
	return r
}

// (a0+a1x+a2x^2)+(b0+b1x+b2x^2)
func polyAdd(p1 []*big.Int, p2 []*big.Int) []*big.Int {
	if len(p1) > len(p2) {
		r := make([]*big.Int, len(p1))
		for i := 0; i < len(p2); i++ {
			r[i] = new(big.Int).Add(p1[i], p2[i])
		}
		for i := len(p2); i < len(p1); i++ {
			r[i] = new(big.Int).Set(p1[i])
		}
		return r
	} else {
		r := make([]*big.Int, len(p2))
		for i := 0; i < len(p1); i++ {
			r[i] = new(big.Int).Add(p1[i], p2[i])
		}
		for i := len(p1); i < len(p2); i++ {
			r[i] = new(big.Int).Set(p2[i])
		}
		return r
	}
}

func Vandermonde(matrix [][]int) (int, []int) {
	// Compute D, D1
	d, coeff := determinant(matrix, len(matrix))
	g := d
	for i := 0; i < len(coeff); i++ {
		g = gcd(g, coeff[i])
	}
	d = d / g
	for i := 0; i < len(coeff); i++ {
		coeff[i] = coeff[i] / g
	}
	if d < 0 {
		d = -d
		for i := 0; i < len(coeff); i++ {
			coeff[i] = -coeff[i]
		}
	}
	return d, coeff
}

func determinant(matrix [][]int, order int) (int, []int) {
	value := 0
	coeff := make([]int, order)
	sign := 1
	if order == 1 {
		value = matrix[0][0]
		coeff[0] = 1
	} else {
		for i := 0; i < order; i++ {
			cofactor := laplace(matrix, i, 0, order)
			value += sign * matrix[i][0] * cofactor
			coeff[i] = sign * cofactor
			sign *= -1
		}
	}
	return value, coeff
}

func laplace(matrix [][]int, r int, c int, order int) int {
	result := 0
	cofactor := make([][]int, order)
	for i := 0; i < order; i++ {
		cofactor[i] = make([]int, order)
	}
	for i := 0; i < order; i++ {
		for j := 0; j < order; j++ {
			tmpi := i
			tmpj := j
			if i != r && j != c {
				if i > r {
					i--
				}
				if j > c {
					j--
				}
				cofactor[i][j] = matrix[tmpi][tmpj]
				i = tmpi
				j = tmpj
			}
		}
	}
	if order >= 2 {
		result, _ = determinant(cofactor, order-1)
	}
	return result
}

func pkcs7Padding(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

func pkcs7UnPadding(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, errors.New("empty array")
	}
	unPadding := int(data[length-1])
	if length-unPadding < 0 {
		return nil, errors.New("unpadding failed")
	}
	return data[:(length - unPadding)], nil
}

func gcd(a, b int) int {
	if b == 0 {
		return a
	}
	if b < 0 {
		b = -b
	}
	return gcd(b, a%b)
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
