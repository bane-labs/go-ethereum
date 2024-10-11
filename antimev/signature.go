package antimev

import (
	"errors"
	"math"

	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	ErrSigShareNotEnough = errors.New("crypto/tpke: sig share not enough")
	ErrSigAggregation    = errors.New("crypto/tpke: sig aggregation failed")
)

// SignShare tries to sign a message with local private key.
func (ks *KeyStore) SignShare(msg []byte) (*tpke.SignatureShare, error) {
	if ks.shared == nil || ks.shared.localPrvKey == nil {
		return nil, ErrNoPrvKey
	}
	return ks.shared.localPrvKey.SignShare(msg), nil
}

// AggregateAndVerifySig tries to aggregate signature shares and returns
// the final signature if the verification passes. The key of inputs is
// dkg index which starts from 1, when the array index of a member in the
// key group starts from 0.
func (ks *KeyStore) AggregateAndVerifySig(msg []byte, inputs map[int]*tpke.SignatureShare) (*tpke.Signature, error) {
	if ks.shared == nil {
		return nil, ErrNoPubKey
	}
	return ks.shared.aggregateAndVerifySig(msg, inputs, ks.threshold, ks.scaler)
}

// aggregateAndVerifySig tries to aggregate signature shares and returns
// the final signature if the verification passes.
func (tkg *thresholdKeyGroup) aggregateAndVerifySig(msg []byte, inputs map[int]*tpke.SignatureShare, threshold int, scaler int) (*tpke.Signature, error) {
	if len(inputs) < threshold {
		return nil, ErrSigShareNotEnough
	}
	if tkg.globalPubKey == nil {
		return nil, ErrNoPubKey
	}

	matrix := make([][]int, len(inputs))                // size=len(inputs)*threshold, including all rows
	shares := make([]*tpke.SignatureShare, len(inputs)) // size=len(inputs), including all shares

	// Be aware of a random order of sig shares
	i := 0
	for index, v := range inputs {
		row := make([]int, threshold)
		for j := 0; j < threshold; j++ {
			row[j] = int(math.Pow(float64(index), float64(j)))
		}
		matrix[i] = row
		shares[i] = v
		i++
	}

	// Use different combinations to verify
	combs := getCombs(len(inputs), threshold)
	for _, v := range combs {
		m := make([][]int, threshold)                // size=threshold*threshold, only seleted rows
		s := make([]*tpke.SignatureShare, threshold) // size=threshold, only seleted shares
		for i := 0; i < len(v); i++ {
			m[i] = matrix[v[i]]
			s[i] = shares[v[i]]
		}
		sig, err := tpke.AggregateSigShares(m, s, scaler)
		// Verify if the aggregation is valid
		if err == nil && tkg.globalPubKey.VerifySig(msg, sig) {
			return sig, nil
		}
	}
	// Return error since this share mapping is not able to recover the signature
	return nil, ErrSigAggregation
}
