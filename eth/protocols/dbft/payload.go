// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"golang.org/x/crypto/sha3"
)

// Message is any broadcasted DBFT message.
type Message struct {
	// ValidBlockStart is the starting height for a payload to be valid.
	ValidBlockStart uint64
	// ValidBlockEnd is the height after which a payload becomes invalid.
	ValidBlockEnd uint64
	// Sender is the payload sender or signer.
	Sender common.Address
	// Data is custom payload data.
	Data []byte
	// Witness is payload signature.
	Witness []byte
}

var invalidSig = errors.New("invalid signature")

// Hash returns hash of the signed part of the [Message].
func (m Message) Hash() common.Hash {
	var h common.Hash

	m.Witness = nil // m is a copy and witness is not hashed.
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, m)
	hw.Sum(h[:0])
	return h
}

// Verify ensures that [Message] is signed (has appropriate witness) by its sender.
func (m Message) Verify() error {
	var h = m.Hash()

	pk, err := crypto.SigToPub(h[:], m.Witness)
	if err != nil {
		return err
	}
	if crypto.PubkeyToAddress(*pk) != m.Sender {
		return invalidSig
	}
	return nil
}
