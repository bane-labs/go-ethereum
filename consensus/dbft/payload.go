package dbft

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/common"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft"
)

type (
	messageType byte

	message struct {
		Type           messageType
		BlockIndex     uint64
		ValidatorIndex byte
		ViewNumber     byte
		msgPayload     interface{}
	}

	// messageAux is an auxiliary structure for message RLP encoding.
	messageAux struct {
		Type           messageType
		BlockIndex     uint64
		ValidatorIndex byte
		ViewNumber     byte
		// rlp encoded bytes of msgPayload
		MsgPayload rlp.RawValue
	}

	// Payload is a type for consensus-related messages.
	Payload struct {
		dbftproto.Message
		message
	}
)

const (
	changeViewType      messageType = 0x00
	prepareRequestType  messageType = 0x20
	prepareResponseType messageType = 0x21
	commitType          messageType = 0x30
	preCommitType       messageType = 0x31
	recoveryRequestType messageType = 0x40
	recoveryMessageType messageType = 0x41
)

var _ dbft.ConsensusPayload[common.Hash] = (*Payload)(nil)

// ViewNumber implements the payload.ConsensusPayload interface.
func (p Payload) ViewNumber() byte {
	return p.message.ViewNumber
}

// Type implements the payload.ConsensusPayload interface.
func (p Payload) Type() dbft.MessageType {
	return dbft.MessageType(p.message.Type)
}

// Payload implements the payload.ConsensusPayload interface.
func (p Payload) Payload() any {
	return p.msgPayload
}

// GetChangeView implements the payload.ConsensusPayload interface.
func (p Payload) GetChangeView() dbft.ChangeView {
	return p.msgPayload.(*changeView)
}

// GetPrepareRequest implements the payload.ConsensusPayload interface.
func (p Payload) GetPrepareRequest() dbft.PrepareRequest[common.Hash] {
	return p.msgPayload.(*prepareRequest)
}

// GetPrepareResponse implements the payload.ConsensusPayload interface.
func (p Payload) GetPrepareResponse() dbft.PrepareResponse[common.Hash] {
	return p.msgPayload.(*prepareResponse)
}

// GetPreCommit implements the payload.ConsensusPayload interface.
func (p Payload) GetPreCommit() dbft.PreCommit {
	return p.msgPayload.(*preCommit)
}

// GetCommit implements the payload.ConsensusPayload interface.
func (p Payload) GetCommit() dbft.Commit {
	return p.msgPayload.(*commit)
}

// GetRecoveryRequest implements the payload.ConsensusPayload interface.
func (p Payload) GetRecoveryRequest() dbft.RecoveryRequest {
	return p.msgPayload.(*recoveryRequest)
}

// GetRecoveryMessage implements the payload.ConsensusPayload interface.
func (p Payload) GetRecoveryMessage() dbft.RecoveryMessage[common.Hash] {
	return p.msgPayload.(*recoveryMessage)
}

// ValidatorIndex implements the payload.ConsensusPayload interface.
func (p Payload) ValidatorIndex() uint16 {
	return uint16(p.message.ValidatorIndex)
}

// SetValidatorIndex implements the payload.ConsensusPayload interface.
func (p *Payload) SetValidatorIndex(i uint16) {
	p.message.ValidatorIndex = byte(i)
}

// Height implements the payload.ConsensusPayload interface.
func (p Payload) Height() uint32 {
	return uint32(p.message.BlockIndex)
}

// Sign signs payload using the private key.
// It also sets corresponding sender and witness.
func (p *Payload) Sign(key *Signer) error {
	if key.Signer != p.Sender {
		return fmt.Errorf("can't sign payload with invalid sender: payload sender must be %s, address for signing is %s", p.Sender, key.Signer)
	}
	p.encodeData()

	b, err := p.rlp()
	if err != nil {
		return fmt.Errorf("failed to calculate RLP: %w", err)
	}
	sig, err := key.Sign(b)
	if err != nil {
		return err
	}

	p.Witness = sig
	return nil
}

// rlp returns serialized hashable payload fields that should be signed.
func (p *Payload) rlp() ([]byte, error) {
	cp := p.Message
	cp.Witness = nil // cp is a copy and witness is not included into hash.

	b := new(bytes.Buffer)
	if err := rlp.Encode(b, cp); err != nil {
		return nil, fmt.Errorf("failed to encode message: %w", err)
	}
	return b.Bytes(), nil
}

// Hash implements the payload.ConsensusPayload interface.
func (p *Payload) Hash() common.Hash {
	if p.Message.Data == nil {
		p.encodeData()
	}
	return p.Message.Hash()
}

// String implements fmt.Stringer interface.
func (t messageType) String() string {
	switch t {
	case changeViewType:
		return "ChangeView"
	case prepareRequestType:
		return "PrepareRequest"
	case prepareResponseType:
		return "PrepareResponse"
	case commitType:
		return "Commit"
	case recoveryRequestType:
		return "RecoveryRequest"
	case recoveryMessageType:
		return "RecoveryMessage"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02x)", byte(t))
	}
}

// DecodeRLP decodes a message from RLP.
func (m *message) DecodeRLP(s *rlp.Stream) error {
	var em messageAux
	if err := s.Decode(&em); err != nil {
		return err
	}
	m.Type, m.BlockIndex, m.ValidatorIndex, m.ViewNumber = em.Type, em.BlockIndex, em.ValidatorIndex, em.ViewNumber
	switch m.Type {
	case changeViewType:
		m.msgPayload = &changeView{
			// newViewNumber is not marshaled
			newViewNumber: m.ViewNumber + 1,
		}
	case prepareRequestType:
		m.msgPayload = new(prepareRequest)
	case prepareResponseType:
		m.msgPayload = new(prepareResponse)
	case preCommitType:
		m.msgPayload = new(preCommit)
	case commitType:
		m.msgPayload = &commit{}
	case recoveryRequestType:
		m.msgPayload = new(recoveryRequest)
	case recoveryMessageType:
		m.msgPayload = new(recoveryMessage)
	default:
		err := fmt.Errorf("invalid type: 0x%02x", byte(m.Type))
		return err
	}
	return rlp.DecodeBytes(em.MsgPayload, m.msgPayload)
}

// EncodeRLP serializes a message as RLP.
func (m *message) EncodeRLP(w io.Writer) error {
	bytes, err := rlp.EncodeToBytes(m.msgPayload)
	if err != nil {
		return err
	}

	return rlp.Encode(w, &messageAux{
		Type:           m.Type,
		BlockIndex:     m.BlockIndex,
		ValidatorIndex: m.ValidatorIndex,
		ViewNumber:     m.ViewNumber,
		MsgPayload:     bytes,
	})
}

func (p *Payload) encodeData() {
	if p.Message.Data == nil {
		p.Message.ValidBlockStart = 0
		p.Message.ValidBlockEnd = p.BlockIndex
		data, err := rlp.EncodeToBytes(&p.message)
		if err != nil {
			panic(fmt.Errorf("failed to encode payload: %w", err))
		}
		p.Message.Data = data
	}
}

// decode data of payload into its message.
func (p *Payload) decodeData() error {
	return rlp.DecodeBytes(p.Message.Data, &p.message)
}
