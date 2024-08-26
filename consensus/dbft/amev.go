package dbft

import (
	"bytes"

	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
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
