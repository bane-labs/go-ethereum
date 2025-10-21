package antimev

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/params"
)

// MinEncryptedGasLimit is the minimum required gas limit for encrypted transaction.
// It is set to be equal to a simple transfer execution cost since it is assumed that
// minimum valid encrypted transaction structure is a simple transfer.
const MinEncryptedGasLimit = uint32(params.TxGas)

var (
	// EncryptedDataPrefix is the prefix of Envelope transaction's data. It's used to
	// distinguish simple transactions that have GovernanceRewardProxy contract as a
	// receiver from Envelope transactions carrying encrypted information inside. In
	// future this prefix may be used for encrypted content versioning.
	EncryptedDataPrefix = []byte{0xff, 0xff, 0xff, 0xff}

	// EncryptedDataPrefixLen is the length of EncryptedDataPrefix.
	EncryptedDataPrefixLen = len(EncryptedDataPrefix)

	// EncryptedDataRoundLen is the amount of bytes that encoded DKG round of transaction
	// encryption takes in the Envelope's data byte slice (the size of Uint32).
	EncryptedDataRoundLen = 4

	// EncryptedDataGasLen is the amount of bytes that encoded gas space to reserve for
	// the decrypted transaction in the block (the size of Uint32).
	EncryptedDataGasLen = 4

	// EncryptedDataHashLen is the amount of bytes that represents the hash of an
	// encrypted transaction.
	EncryptedDataHashLen = common.HashLength

	// minEncryptedDataSize is the minimum size of encrypted data stored in the
	// Envelope transaction. It consists of the constant-length prefix,
	// constant-length CipherText and variable-length encrypted message. The size of
	// a simple gas transfer with 1 gwei (105 bytes) is taken as a reference point
	// for evaluation of variable-length part; it is padded to be even to the AES
	// block size as required by AES encryption rules.
	minEncryptedDataSize = EncryptedDataPrefixLen + EncryptedDataRoundLen + EncryptedDataGasLen + EncryptedDataHashLen + tpke.CipherTextSize + 105 + (aes.BlockSize - 105%aes.BlockSize)
)

// IsEnvelope checks whether a transaction is an Envelope transaction. The criteria
// include receiver's address, data prefix and data length check.
func IsEnvelope(tx *types.Transaction) bool {
	return (tx.Type() != types.BlobTxType && tx.Type() != types.SetCodeTxType) && IsEnvelopeToAddress(tx.To()) && IsEnvelopeData(tx.Data())
}

// IsEnvelopeToAddress checks whether an address pointer has the expected value for
// Envelope To address.
func IsEnvelopeToAddress(addr *common.Address) bool {
	if addr == nil || *addr != systemcontracts.GovernanceRewardProxyHash {
		return false
	}
	return true
}

// IsEnvelopeData checks whether the input data bytes has the expected prefix for
// Envelope specification.
func IsEnvelopeData(data []byte) bool {
	if len(data) < minEncryptedDataSize || !bytes.HasPrefix(data, EncryptedDataPrefix) {
		return false
	}
	return true
}

// GetEncryptedHash returns the hash of inner encrypted transaction specified in an
// unencrypted part of Envelope data. Passing non-Envelope as an argument is a no-op.
func GetEncryptedHash(envelope *types.Transaction) common.Hash {
	hashOffset := EncryptedDataPrefixLen + EncryptedDataRoundLen + EncryptedDataGasLen
	return common.Hash(envelope.Data()[hashOffset : hashOffset+EncryptedDataHashLen])
}

// GetEncryptedGas returns the gas limit of inner encrypted transaction specified in an
// unencrypted part of Envelope data. Passing non-Envelope as an argument is a no-op.
func GetEncryptedGas(envelopeData []byte) uint32 {
	gasOffset := EncryptedDataPrefixLen + EncryptedDataRoundLen
	return binary.BigEndian.Uint32(envelopeData[gasOffset : gasOffset+EncryptedDataGasLen])
}
