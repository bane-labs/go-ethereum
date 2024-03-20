package dbft

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbftCrypto "github.com/nspcc-dev/dbft/crypto"
)

var _ = dbftCrypto.PrivateKey(&Signer{})

type (
	// DataSignerFn hashes and signs the data to be signed by a backing account.
	DataSignerFn func(signer accounts.Account, mimeType string, message []byte) ([]byte, error)

	// TxSignerFn signs the provided transaction by a backing account.
	TxSignerFn func(account accounts.Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
)

// Signer is a wrapper around Eth signer function that implements dbftCrypto.PrivateKey
// interface and is sufficient for dBFT operations.
type Signer struct {
	Signer common.Address
	SignFn DataSignerFn
}

// Sign implements dbftCrypto.PrivateKey interface and signs the given message.
// In case of block, msg is expected to be dbftRLP(header) that must be
// guaranteed by Block.Sign method.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, msg)
}
