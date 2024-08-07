package dbft

import "github.com/ethereum/go-ethereum/core/types"

func isEnvelope(tx *types.Transaction) bool {
	return false
}

// Envelope is an interface representing encrypted enveloped transaction.
type Envelope interface {
	// Decrypt decrypts the encripted content of the Envelope transaction using the
	// provided shared key and returns two transactions: the original outer one and
	// an unencrypted inner one.
	Decrypt(sharedKey any) (*types.Transaction, *types.Transaction)
}
