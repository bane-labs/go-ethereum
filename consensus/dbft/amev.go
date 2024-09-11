package dbft

import (
	"bytes"
	"crypto/aes"
	"fmt"

	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	// encryptedDataPrefix is the prefix of Envelope transaction's data. It's used to
	// distinguish simple transactions that have GovernanceRewardProxy contract as a
	// receiver from Envelope transactions carrying encrypted information inside. In
	// future this prefix may be used for encrypted content versioning.
	encryptedDataPrefix = []byte{0xff, 0xff, 0xff, 0xff}

	// encryptedDataPrefixLen is the length of encryptedDataPrefix.
	encryptedDataPrefixLen = len(encryptedDataPrefix)

	// minEncryptedDataSize is the minimum size of encrypted data stored in the
	// Envelope transaction. It consists of the constant-length prefix,
	// constant-length CipherText and variable-length encrypted message. The size of
	// a simple gas transfer with 1 gwei (105 bytes) is taken as a reference point
	// for evaluation of variable-length part; it is padded to be even to the AES
	// block size as required by AES encryption rules.
	minEncryptedDataSize = encryptedDataPrefixLen + tpke.CipherTextSize + 105 + (aes.BlockSize - 105%aes.BlockSize)
)

// isEnvelope checks whether a transaction is an Envelope transaction. The criteria
// include receiver's address, data prefix and data length check.
func isEnvelope(tx *types.Transaction) bool {
	if tx.To() == nil || *(tx.To()) != systemcontracts.GovernanceRewardProxyHash {
		return false
	}

	data := tx.Data()
	if len(data) < minEncryptedDataSize || !bytes.HasPrefix(data, encryptedDataPrefix) {
		return false
	}

	return true
}

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
