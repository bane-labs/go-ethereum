package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

// envelopeData is a structure used for Envelope transaction's data serialization.
type envelopeData struct {
	// index is an index of the corresponding Envelope transaction in the block. This
	// field should be filled in during Envelope parsing and is not included into
	// serialized Envelope bytes.
	index int
	// prefix is a 4-bytes prefix used for Envelope's data versioning.
	prefix []byte
	// encryptedKey is a tpke.CipherText provided by the sender of encrypted
	// transaction.
	encryptedKey *tpke.CipherText
	// encryptedMsg contains the encrypted transaction itself.
	encryptedMsg []byte
}

// decodeEnvelopeData decodes envelopeData from the provided slice. It's a no-op to
// pass not an Envelope's data.
func decodeEnvelopeData(buf []byte) (envelopeData, error) {
	encryptedDataPrefixLen := len(antimev.EncryptedDataPrefix)
	var key = new(tpke.CipherText)
	// It's guaranteed by Envelope definition that buf has a proper length.
	_, err := key.FromBytes(buf[encryptedDataPrefixLen : encryptedDataPrefixLen+tpke.CipherTextSize])
	if err != nil {
		return envelopeData{}, fmt.Errorf("failed to decode TPKE cipher text: %w", err)
	}
	return envelopeData{
		prefix:       buf[:encryptedDataPrefixLen],
		encryptedKey: key,
		encryptedMsg: buf[encryptedDataPrefixLen+tpke.CipherTextSize:],
	}, nil
}
