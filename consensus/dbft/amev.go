package dbft

import (
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"

	"bytes"
)

const (
	// txMinSize is the minimum size a single transaction can have.
	// The size of a simple gas transfer with 1 gwei is 105 bytes, we use this as the minimum tx.
	txMinSize = 105
)

// encryptDataPrefix is the prefix of Envelope tx data
var encryptDataPrefix = []byte{0xff, 0xff, 0xff, 0xff}

// isEnvelope checks whether a transaction is an envelope transaction,
// including to address, data prefix and data length check.
func isEnvelope(tx *types.Transaction) bool {
	if tx.To() == nil || *(tx.To()) != systemcontracts.GovernanceRewardProxyHash {
		return false
	}

	if len(tx.Data()) < txMinSize || !bytes.HasPrefix(tx.Data(), encryptDataPrefix) {
		return false
	}

	return true
}

// decodeEnvelopesData finds Envelope transactions in the provided list and returns
// their data in deserialized form.
func decodeEnvelopesData(txs []*types.Transaction) []envelopeData {
	var res []envelopeData
	for i, tx := range txs {
		if isEnvelope(tx) {
			d, err := decodeEnvelopeData(tx.Data())
			if err != nil {
				continue
			}
			d.index = i
			res = append(res, d)
		}
	}
	return res
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

func decodeEnvelopeData([]byte) (envelopeData, error) {
	// TODO: implement RLP encoding/decoding for envelopeData
	panic("TODO")
}
