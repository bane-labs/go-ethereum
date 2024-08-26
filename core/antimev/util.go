package antimev

import (
	"crypto/rand"
	"math"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

// randPG1 returns a random bls12381 g1 point
func randPG1() *bls12381.G1Affine {
	r := randScalar()
	return new(bls12381.G1Affine).ScalarMultiplicationBase(r)
}

// randScalar returns a random big int
func randScalar() *big.Int {
	a, _ := rand.Int(rand.Reader, ecc.BLS12_381.ScalarField())
	return a
}

// getScaler returns a scaler factor for public key, works under size 7,
// but need to be carefully verified in a larger size.
func getScaler(size int, threshold int) int {
	matrix := make([][]int, threshold) // size=threshold*threshold
	return searchDLCM(matrix, 1, 0, 0, size, threshold)
}

// searchDLCM tries to find a minimum value of scaler for the given matrix.
func searchDLCM(matrix [][]int, l, pos, offset, size, threshold int) int {
	if pos == threshold {
		d, coeff := tpke.Feldman(matrix)
		g := d
		for i := 0; i < len(coeff); i++ {
			g = gcd(g, coeff[i])
		}
		d = d / g
		return abs(d)
	}
	for i := pos + offset; i < size-threshold+pos+1; i++ {
		row := make([]int, threshold)
		for j := 0; j < threshold; j++ {
			row[j] = int(math.Pow(float64(i+1), float64(j)))
		}
		matrix[pos] = row
		l = lcm(l, searchDLCM(matrix, l, pos+1, i-pos, size, threshold))
	}
	return l
}

// getCombs returns all possible combinations to take n of m.
func getCombs(m int, n int) [][]int {
	return searchCombs(make([]int, n), 0, 0, m, n)
}

// searchDLCM tries to find possible combinations to take n of m.
func searchCombs(arr []int, pos, offset, m, n int) [][]int {
	results := make([][]int, 0)
	if pos == n {
		comb := make([]int, n)
		copy(comb, arr)
		results = append(results, comb)
		return results
	}
	for i := pos + offset; i < m; i++ {
		arr[pos] = i
		results = append(results, searchCombs(arr, pos+1, i-pos, m, n)...)
	}
	return results
}

// gcd returns the greatest common divisor of a and b.
func gcd(a, b int) int {
	if b == 0 {
		return a
	}
	if b < 0 {
		b = -b
	}
	return gcd(b, a%b)
}

// lcm returns the least common multiple of a and b.
func lcm(a, b int) int {
	return a * b / gcd(a, b)
}

// abs returns the absolute value of a.
func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
