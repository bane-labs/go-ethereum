package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft/dbftutil"
	"github.com/ethereum/go-ethereum/core/types"
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

// signBlock signs block RLP bytes using the given extra version and signing scheme.
func (s *Signer) signBlock(extra dbftutil.Extra, header *types.Header) ([]byte, error) {
	switch v := extra.Version(); v {
	case dbftutil.ExtraV0:
		return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, dbftRLP(header))
	case dbftutil.ExtraV1, dbftutil.ExtraV2, dbftutil.ExtraV3:
		var encode []byte
		if v == dbftutil.ExtraV1 || v == dbftutil.ExtraV2 {
			encode = dbftRLP(header)
		} else {
			encode = dbftSSZ(header)
		}
		switch ss := extra.SignatureScheme(); ss {
		case dbftutil.ECDSAScheme:
			return s.SignFn(accounts.Account{Address: s.Signer}, accounts.MimetypeTextPlain, encode)
		case dbftutil.ThresholdScheme:
			share, err := s.AmevKeystore.SignShare(encode, v == dbftutil.ExtraV1)
			if err != nil {
				return nil, fmt.Errorf("failed to sign share: %w", err)
			}
			return share.Bytes(), nil
		default:
			return nil, fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedBlockSignatureScheme, ss)
		}
	default:
		return nil, fmt.Errorf("%w: %d", dbftutil.ErrUnexpectedExtraVersion, v)
	}
}
