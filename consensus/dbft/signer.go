package dbft

import (
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/nspcc-dev/dbft"
)

var _ = dbft.PrivateKey(&Signer{})

// SignerFn hashes and signs the data to be signed by a backing account.
type SignerFn func(signer accounts.Account, mimeType string, message []byte) ([]byte, error)

// Signer is a wrapper around Eth signer function that implements dbftCrypto.PrivateKey
// interface and is sufficient for dBFT operations.
type Signer struct {
	Signer common.Address
	SignFn SignerFn
}

// Sign implements dbftCrypto.PrivateKey interface and signs the given message.
// In case of block, msg is expected to be dbftRLP(header) that must be
// guaranteed by Block.Sign method.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, msg)
}
