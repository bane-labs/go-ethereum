package antimev

import (
	"bytes"
	"crypto/aes"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	// EncryptedDataPrefix is the prefix of Envelope transaction's data. It's used to
	// distinguish simple transactions that have GovernanceRewardProxy contract as a
	// receiver from Envelope transactions carrying encrypted information inside. In
	// future this prefix may be used for encrypted content versioning.
	EncryptedDataPrefix = []byte{0xff, 0xff, 0xff, 0xff}

	// EncryptedDataPrefixLen is the length of EncryptedDataPrefix.
	EncryptedDataPrefixLen = len(EncryptedDataPrefix)

	// EncryptedDataRoundLen is the amount of bytes that encoded DKG round of transaction
	// encryption takes in the Envelope's data byte slice (the size of Uint64).
	EncryptedDataRoundLen = 4

	// EncryptedDataHashLen is the amount of bytes that represents the hash of an
	// encrypted transaction.
	EncryptedDataHashLen = common.HashLength

	// minEncryptedDataSize is the minimum size of encrypted data stored in the
	// Envelope transaction. It consists of the constant-length prefix,
	// constant-length CipherText and variable-length encrypted message. The size of
	// a simple gas transfer with 1 gwei (105 bytes) is taken as a reference point
	// for evaluation of variable-length part; it is padded to be even to the AES
	// block size as required by AES encryption rules.
	minEncryptedDataSize = EncryptedDataPrefixLen + EncryptedDataRoundLen + EncryptedDataHashLen + tpke.CipherTextSize + 105 + (aes.BlockSize - 105%aes.BlockSize)
)

// IsEnvelope checks whether a transaction is an Envelope transaction. The criteria
// include receiver's address, data prefix and data length check.
func IsEnvelope(tx *types.Transaction) bool {
	if tx.To() == nil || *(tx.To()) != systemcontracts.GovernanceRewardProxyHash {
		return false
	}

	data := tx.Data()
	if len(data) < minEncryptedDataSize || !bytes.HasPrefix(data, EncryptedDataPrefix) {
		return false
	}

	return true
}

// GetEncryptedHash returns the hash of inner encrypted transaction specified in an
// unencrypted part of Envelope data. Passing non-Envelope as an argument is a no-op.
func GetEncryptedHash(envelope *types.Transaction) common.Hash {
	hashOffset := EncryptedDataPrefixLen + EncryptedDataRoundLen
	return common.Hash(envelope.Data()[hashOffset : hashOffset+EncryptedDataHashLen])
}
