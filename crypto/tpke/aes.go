package tpke

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
)

var (
	ErrAESMessage    = errors.New("crypto/tpke: empty aes message")
	ErrAESCiphertext = errors.New("crypto/tpke: empty aes ciphertext")
	ErrAESEncryption = errors.New("crypto/tpke: aes encryption faild")
	ErrAESDecryption = errors.New("crypto/tpke: aes decryption faild")
)

// AESEncrypt uses a bls g1 point as a key to encrypt the input message
func AESEncrypt(pg1 *bls12381.PointG1, msg []byte) ([]byte, error) {
	if len(msg) < 1 {
		return nil, ErrAESMessage
	}
	// Take pg1 as the input of sha256 to generate an aes key
	seed := bls12381.NewG1().ToBytes(pg1)
	hash := sha256.Sum256(seed[0:96])
	block, err := aes.NewCipher(hash[0:32])
	if err != nil {
		return nil, ErrAESEncryption
	}
	blockSize := block.BlockSize()

	data := pkcs7Padding(msg, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, hash[:blockSize])
	encrypted := make([]byte, len(data))
	blockMode.CryptBlocks(encrypted, data)

	return encrypted, nil
}

// AESDecrypt uses a bls g1 point as a key to decrypt the input ciphertext
func AESDecrypt(pg1 *bls12381.PointG1, cipherText []byte) ([]byte, error) {
	if len(cipherText) < 1 {
		return nil, ErrAESCiphertext
	}
	// Take pg1 as the input of sha256 to generate an aes key
	seed := bls12381.NewG1().ToBytes(pg1)
	hash := sha256.Sum256(seed[0:96])
	block, err := aes.NewCipher(hash[0:32])
	if err != nil {
		return nil, ErrAESDecryption
	}
	blockSize := block.BlockSize()

	blockMode := cipher.NewCBCDecrypter(block, hash[:blockSize])
	decrypted := make([]byte, len(cipherText))
	blockMode.CryptBlocks(decrypted, cipherText)
	result, err := pkcs7UnPadding(decrypted)
	if err != nil {
		return nil, err
	}

	return result, nil
}
