package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft/dbftutil"
	"github.com/nspcc-dev/dbft"
)

var _ = dbft.PrivateKey(&Signer{})

// SignerFn hashes and signs the data to be signed by a backing account.
type SignerFn func(signer accounts.Account, mimeType string, message []byte) ([]byte, error)

// Signer is a wrapper around Eth signer function that implements dbftCrypto.PrivateKey
// interface and is sufficient for dBFT operations.
type Signer struct {
	Signer       common.Address
	SignFn       SignerFn
	AmevKeystore *antimev.KeyStore
}

// Sign implements dbftCrypto.PrivateKey interface and signs the given message.
// Sign expects consensus message bytes as an input; for block signing use
// [Signer.signBlock].
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, msg)
}

// signBlock signs block RLP bytes using the given signing scheme.
func (s *Signer) signBlock(scheme dbftutil.ExtraVersion, blockRLP []byte) ([]byte, error) {
	switch scheme {
	case dbftutil.ExtraV0:
		return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, blockRLP)
	case dbftutil.ExtraV1:
		share, err := s.AmevKeystore.SignShare(blockRLP)
		if err != nil {
			return nil, fmt.Errorf("failed to sign share: %w", err)
		}
		return share.ToBytes(), nil
	default:
		return nil, fmt.Errorf("unsupported signature scheme: %d", scheme)
	}
}
