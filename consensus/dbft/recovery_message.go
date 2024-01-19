package dbft

import (
	"errors"
	"io"

	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft/crypto"
	"github.com/nspcc-dev/dbft/payload"
	"github.com/nspcc-dev/neo-go/pkg/util"
)

type (
	// recoveryMessage represents dBFT Recovery message.
	recoveryMessage struct {
		PreparationPayloads []*preparationCompact
		CommitPayloads      []*commitCompact
		ChangeViewPayloads  []*changeViewCompact
		PreparationHashExt  *util.Uint256
		PrepareRequest      *message
	}

	// recoveryMessageAux is an auxiliary structure for recoveryMessage RLP encoding.
	recoveryMessageAux struct {
		PreparationPayloads []*preparationCompact
		CommitPayloads      []*commitCompact
		ChangeViewPayloads  []*changeViewCompact
		PreparationHashExt  *util.Uint256 `rlp:"optional"`
		PrepareRequest      *message      `rlp:"optional"`
	}

	changeViewCompact struct {
		ValidatorIndex     uint8
		OriginalViewNumber byte
		Timestamp          uint64
		InvocationScript   []byte
	}

	commitCompact struct {
		ViewNumber       byte
		ValidatorIndex   uint8
		Signature        [extraSeal]byte
		InvocationScript []byte
	}

	preparationCompact struct {
		ValidatorIndex   uint8
		InvocationScript []byte
	}
)

var _ payload.RecoveryMessage = (*recoveryMessage)(nil)

// AddPayload implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) AddPayload(p payload.ConsensusPayload) {
	validator := uint8(p.ValidatorIndex())

	switch p.Type() {
	case payload.PrepareRequestType:
		m.PrepareRequest = &message{
			Type:       prepareRequestType,
			ViewNumber: p.ViewNumber(),
			msgPayload: p.GetPrepareRequest().(*prepareRequest),
		}
		h := p.Hash()
		m.PreparationHashExt = &h
		m.PreparationPayloads = append(m.PreparationPayloads, &preparationCompact{
			ValidatorIndex:   validator,
			InvocationScript: p.(*Payload).Witness,
		})
	case payload.PrepareResponseType:
		m.PreparationPayloads = append(m.PreparationPayloads, &preparationCompact{
			ValidatorIndex:   validator,
			InvocationScript: p.(*Payload).Witness,
		})

		if m.PreparationHashExt == nil {
			h := p.GetPrepareResponse().PreparationHash()
			m.PreparationHashExt = &h
		}
	case payload.ChangeViewType:
		m.ChangeViewPayloads = append(m.ChangeViewPayloads, &changeViewCompact{
			ValidatorIndex:     validator,
			OriginalViewNumber: p.ViewNumber(),
			Timestamp:          p.GetChangeView().Timestamp(),
			InvocationScript:   p.(*Payload).Witness,
		})
	case payload.CommitType:
		m.CommitPayloads = append(m.CommitPayloads, &commitCompact{
			ValidatorIndex:   validator,
			ViewNumber:       p.ViewNumber(),
			Signature:        p.GetCommit().(*commit).SignatureExt,
			InvocationScript: p.(*Payload).Witness,
		})
	}
}

// GetPrepareRequest implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetPrepareRequest(p payload.ConsensusPayload, validators []crypto.PublicKey, primary uint16) payload.ConsensusPayload {
	if m.PrepareRequest == nil {
		return nil
	}

	var compact *preparationCompact
	for _, p := range m.PreparationPayloads {
		if p != nil && p.ValidatorIndex == uint8(primary) {
			compact = p
			break
		}
	}

	if compact == nil {
		return nil
	}

	req := fromPayload(prepareRequestType, p.(*Payload), m.PrepareRequest.msgPayload)
	req.SetValidatorIndex(primary)
	req.Sender = validators[primary].(*PublicKey).Account
	req.Witness = compact.InvocationScript

	return req
}

// GetPrepareResponses implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetPrepareResponses(p payload.ConsensusPayload, validators []crypto.PublicKey) []payload.ConsensusPayload {
	if m.PreparationHashExt == nil {
		return nil
	}

	ps := make([]payload.ConsensusPayload, len(m.PreparationPayloads))

	for i, resp := range m.PreparationPayloads {
		r := fromPayload(prepareResponseType, p.(*Payload), &prepareResponse{
			PreparationHashExt: *m.PreparationHashExt,
		})
		r.SetValidatorIndex(uint16(resp.ValidatorIndex))
		r.Sender = validators[resp.ValidatorIndex].(*PublicKey).Account
		r.Witness = resp.InvocationScript

		ps[i] = r
	}

	return ps
}

// GetChangeViews implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetChangeViews(p payload.ConsensusPayload, validators []crypto.PublicKey) []payload.ConsensusPayload {
	ps := make([]payload.ConsensusPayload, len(m.ChangeViewPayloads))

	for i, cv := range m.ChangeViewPayloads {
		c := fromPayload(changeViewType, p.(*Payload), &changeView{
			newViewNumber: cv.OriginalViewNumber + 1,
			TimestampExt:  cv.Timestamp,
		})
		c.message.ViewNumber = cv.OriginalViewNumber
		c.SetValidatorIndex(uint16(cv.ValidatorIndex))
		c.Sender = validators[cv.ValidatorIndex].(*PublicKey).Account
		c.Witness = cv.InvocationScript

		ps[i] = c
	}

	return ps
}

// GetCommits implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetCommits(p payload.ConsensusPayload, validators []crypto.PublicKey) []payload.ConsensusPayload {
	ps := make([]payload.ConsensusPayload, len(m.CommitPayloads))

	for i, c := range m.CommitPayloads {
		cc := fromPayload(commitType, p.(*Payload), &commit{SignatureExt: c.Signature})
		cc.SetValidatorIndex(uint16(c.ValidatorIndex))
		cc.Sender = validators[c.ValidatorIndex].(*PublicKey).Account
		cc.Witness = c.InvocationScript

		ps[i] = cc
	}

	return ps
}

// PreparationHash implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) PreparationHash() *util.Uint256 {
	return m.PreparationHashExt
}

// SetPreparationHash implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) SetPreparationHash(h *util.Uint256) {
	m.PreparationHashExt = h
}

func fromPayload(t messageType, recovery *Payload, p any) *Payload {
	return &Payload{
		Message: dbftproto.Message{
			ValidBlockEnd: recovery.BlockIndex,
			Sender:        recovery.Sender,
		},
		message: message{
			Type:       t,
			BlockIndex: recovery.BlockIndex,
			ViewNumber: recovery.message.ViewNumber,
			msgPayload: p,
		},
	}
}

// EncodeRLP serializes recoveryMessage as RLP.
func (m *recoveryMessage) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &recoveryMessageAux{
		PreparationPayloads: m.PreparationPayloads,
		CommitPayloads:      m.CommitPayloads,
		ChangeViewPayloads:  m.ChangeViewPayloads,
		PreparationHashExt:  m.PreparationHashExt,
		PrepareRequest:      m.PrepareRequest,
	})
}

// DecodeRLP decodes recoveryMessage from RLP.
func (m *recoveryMessage) DecodeRLP(s *rlp.Stream) error {
	var aux recoveryMessageAux
	if err := s.Decode(&aux); err != nil {
		return err
	}

	// Perform some validity checks.
	if aux.PrepareRequest != nil && aux.PrepareRequest.Type != prepareRequestType {
		return errors.New("recovery message PrepareRequest has wrong type")
	}

	m.PreparationPayloads = aux.PreparationPayloads
	m.CommitPayloads = aux.CommitPayloads
	m.ChangeViewPayloads = aux.ChangeViewPayloads
	m.PreparationHashExt = aux.PreparationHashExt
	m.PrepareRequest = aux.PrepareRequest
	return nil
}
