package dbft

import (
	"encoding/binary"
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
	// dkgRound is a DKG round index by the moment Envelope's data was encrypted
	// according to the KeyManagement system contract.
	dkgRound uint32
	// encryptedKey is a tpke.CipherText provided by the sender of encrypted
	// transaction.
	encryptedKey *tpke.CipherText
	// encryptedMsg contains the encrypted transaction itself.
	encryptedMsg []byte
}

// decodeEnvelopeData decodes envelopeData from the provided slice. It's a no-op to
// pass not an Envelope's data.
func decodeEnvelopeData(buf []byte) (envelopeData, error) {
	var (
		key              = new(tpke.CipherText)
		keyOffset        = antimev.EncryptedDataPrefixLen + antimev.EncryptedDataRoundLen + antimev.EncryptedDataGasLen + antimev.EncryptedDataHashLen
		cipherTextOffset = keyOffset + tpke.CipherTextSize
	)
	// It's guaranteed by Envelope definition that buf has a proper length.
	_, err := key.FromBytes(buf[keyOffset:cipherTextOffset])
	if err != nil {
		return envelopeData{}, fmt.Errorf("failed to decode TPKE cipher text: %w", err)
	}
	round := binary.BigEndian.Uint32(buf[antimev.EncryptedDataPrefixLen:keyOffset])
	if round == 0 {
		return envelopeData{}, fmt.Errorf("invalid TPKE cipher text: invalid round %d", round)
	}
	return envelopeData{
		prefix:       buf[:antimev.EncryptedDataPrefixLen],
		dkgRound:     round,
		encryptedKey: key,
		encryptedMsg: buf[cipherTextOffset:],
	}, nil
}
