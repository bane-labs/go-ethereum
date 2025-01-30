package antimev

import (
	"errors"
	"fmt"
	"math"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	ErrDecryptionShareNotEnough = errors.New("decryption share not enough")
	ErrDecryptionFailed         = errors.New("decryption failed")
	ErrNoPrvKey                 = errors.New("no local private key")
	ErrNoPubKey                 = errors.New("no global public key")
	ErrLengthMismatch           = errors.New("input length mismatch")
)

// Encrypt uses the current finished sharing to encrypt the input byte array.
// It returns an encrypted aes key and an encrypted byte array.
func (ks *KeyStore) Encrypt(msg []byte) (*tpke.CipherText, []byte, error) {
	if ks.shared == nil {
		return nil, nil, ErrNoPubKey
	}
	// Random AES key
	aesKey := randPG1()
	// Encrypt the key
	encryptedKey, err := ks.shared.encrypt(aesKey)
	if err != nil {
		return nil, nil, err
	}
	// Encrypt the message
	encryptedMsg, err := tpke.AESEncrypt(aesKey, msg)
	if err != nil {
		return nil, nil, err
	}
	return encryptedKey, encryptedMsg, nil
}

// DecryptWithShare generates decryption shares for decrypting an array of
// aes keys. The returned array is ordered the same as inputs.
func (ks *KeyStore) DecryptWithShare(cts []*tpke.CipherText) ([]*tpke.DecryptionShare, error) {
	if ks.shared == nil {
		return nil, ErrNoPrvKey
	}
	return ks.shared.decryptShare(cts)
}

// DecryptWithShare generates decryption shares for decrypting an array of
// aes keys, which is encrypted by old global public key. The returned
// array is ordered the same as inputs.
func (ks *KeyStore) DecryptWithReshare(cts []*tpke.CipherText) ([]*tpke.DecryptionShare, error) {
	if ks.reshared == nil {
		return nil, ErrNoPrvKey
	}
	return ks.reshared.decryptShare(cts)
}

// AggregateAndDecryptWithShare tries to aggregate decryption shares and finally
// decrypt the aes keys and the raw messages. The encrypted keys, encrypted
// messages and decryption shares should have the same input order. The key of
// inputs is dkg index which starts from 1, when the array index of a member in
// the key group starts from 0.
func (ks *KeyStore) AggregateAndDecryptWithShare(cts []*tpke.CipherText, msg [][]byte, inputs map[int]([]*tpke.DecryptionShare)) ([][]byte, error) {
	if ks.shared == nil {
		return nil, ErrNoPubKey
	}
	if len(cts) != len(msg) {
		return nil, fmt.Errorf("%w: %d encrypted keys vs %d encrypted messages", ErrLengthMismatch, len(cts), len(msg))
	}
	for i, v := range inputs {
		if len(v) != len(cts) {
			return nil, fmt.Errorf("%w: validator %d: %d decryption shares vs %d encrypted keys", ErrLengthMismatch, i, len(v), len(cts))
		}
	}
	// Try decrypt the aes keys, err will be nil if the provided shares are valid,
	// but it doesn't promise the following aes decryption will succeed
	aesKeys, err := ks.shared.aggregateAndDecrypt(cts, inputs, ks.threshold, ks.scaler)
	if err != nil {
		return nil, err
	}
	// Decrypt the messages, err will be nil if the user encryption is valid
	decryptedMsgs := make([][]byte, len(msg))
	for i := 0; i < len(decryptedMsgs); i++ {
		// Set nil if the aes fails
		m, _ := tpke.AESDecrypt(aesKeys[i], msg[i])
		decryptedMsgs[i] = m
	}
	return decryptedMsgs, nil
}

// AggregateAndDecryptWithShare tries to aggregate decryption shares and finally
// decrypt the aes keys and the raw messages, but the reshared key group is used.
func (ks *KeyStore) AggregateAndDecryptWithReshare(cts []*tpke.CipherText, msg [][]byte, inputs map[int]([]*tpke.DecryptionShare)) ([][]byte, error) {
	if ks.reshared == nil {
		return nil, ErrNoPubKey
	}
	if len(cts) != len(msg) {
		return nil, ErrLengthMismatch
	}
	for _, v := range inputs {
		if len(v) != len(cts) {
			return nil, ErrLengthMismatch
		}
	}
	// Try decrypt the aes keys, err will be nil if the provided shares are valid,
	// but it doesn't promise the following aes decryption will succeed
	aesKeys, err := ks.reshared.aggregateAndDecrypt(cts, inputs, ks.threshold, ks.scaler)
	if err != nil {
		return nil, err
	}
	// Decrypt the messages, err will be nil if the user encryption is valid
	decryptedMsgs := make([][]byte, len(msg))
	for i := 0; i < len(decryptedMsgs); i++ {
		// Set nil if the aes fails
		raw, _ := tpke.AESDecrypt(aesKeys[i], msg[i])
		decryptedMsgs[i] = raw
	}
	return decryptedMsgs, nil
}

// encrypt encrypts the input bls12381 g1 point which is also an aes key.
func (tkg *thresholdKeyGroup) encrypt(msg *bls12381.G1Affine) (*tpke.CipherText, error) {
	if tkg.globalPubKey == nil {
		return nil, ErrNoPubKey
	}
	return tkg.globalPubKey.Encrypt(msg), nil
}

// decryptShare generates decryption shares for an array of encrypted aes keys.
func (tkg *thresholdKeyGroup) decryptShare(cts []*tpke.CipherText) ([]*tpke.DecryptionShare, error) {
	if tkg.localPrvKey == nil {
		return nil, ErrNoPrvKey
	}
	share := make([]*tpke.DecryptionShare, len(cts))
	for j := 0; j < len(cts); j++ {
		share[j] = tkg.localPrvKey.DecryptShare(cts[j])
	}

	return share, nil
}

// aggregateAndDecrypt tries to aggregate decryption shares and recover an array of
// encrypted aes keys. It returns error if the decryption share input is not able to
// recover the correct data as they are committed. The key of inputs is dkg index
// which starts from 1, when the array index of a member in the key group starts from 0.
func (tkg *thresholdKeyGroup) aggregateAndDecrypt(cts []*tpke.CipherText, inputs map[int]([]*tpke.DecryptionShare), threshold int, scaler int) ([]*bls12381.G1Affine, error) {
	if len(inputs) < threshold {
		return nil, ErrDecryptionShareNotEnough
	}
	if tkg.globalPubKey == nil {
		return nil, ErrNoPubKey
	}

	matrix := make([][]int, len(inputs))                   // size=len(inputs)*threshold, including all rows
	shares := make([][]*tpke.DecryptionShare, len(inputs)) // size=len(inputs)*len(cts), including all shares

	// Be aware of a random order of decryption shares
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

	// Use different combinations to decrypt
	combs := getCombs(len(inputs), threshold)
	for _, v := range combs {
		m := make([][]int, threshold)                   // size=threshold*threshold, only seleted rows
		s := make([][]*tpke.DecryptionShare, threshold) // size=threshold*len(cts), only seleted shares
		for i := 0; i < len(v); i++ {
			m[i] = matrix[v[i]]
			s[i] = shares[v[i]]
		}
		// The parallel verification of decryption is contained, refer to crypto/tpke
		// Any failure here means an invalid decryption combination, should continue
		results, err := tpke.AggregateAndDecrypt(cts, m, s, tkg.globalPubKey, scaler)
		if err == nil {
			return results, nil
		}
	}
	// Return error since this share mapping is not able to recover the message
	return nil, ErrDecryptionFailed
}
