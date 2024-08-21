package tpke

import (
	"bytes"
	"crypto/rand"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

func randScalar() *big.Int {
	a, _ := rand.Int(rand.Reader, bls12381.NewGT().Q())
	return a
}

func randPG1() *bls12381.PointG1 {
	r := randScalar()
	g1 := bls12381.NewG1()
	pg1 := g1.New()
	return g1.MulScalar(pg1, g1.One(), r)
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

func Feldman(matrix [][]int) (int, []int) {
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
