package dbft

import (
	"errors"
	"io"

	"github.com/ethereum/go-ethereum/common"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft"
)

type (
	// recoveryMessage represents dBFT Recovery message.
	recoveryMessage struct {
		PreparationPayloads []*preparationCompact
		PreCommitPayloads   []*preCommitCompact
		CommitPayloads      []*commitCompact
		ChangeViewPayloads  []*changeViewCompact
		PreparationHashExt  *common.Hash
		PrepareRequest      *message
	}

	// recoveryMessageAux is an auxiliary structure for recoveryMessage RLP encoding.
	recoveryMessageAux struct {
		PreparationPayloads []*preparationCompact
		PreCommitPayloads   []*preCommitCompact
		CommitPayloads      []*commitCompact
		ChangeViewPayloads  []*changeViewCompact
		PreparationHashExt  *common.Hash `rlp:"optional"`
		PrepareRequest      *message     `rlp:"optional"`
	}

	changeViewCompact struct {
		ValidatorIndex     uint8
		OriginalViewNumber byte
		Timestamp          uint64
		InvocationScript   []byte
	}

	preCommitCompact struct {
		ViewNumber       byte
		ValidatorIndex   uint8
		Data             []byte
		InvocationScript []byte
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

var _ dbft.RecoveryMessage[common.Hash] = (*recoveryMessage)(nil)

// AddPayload implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) AddPayload(p dbft.ConsensusPayload[common.Hash]) {
	validator := uint8(p.ValidatorIndex())

	switch p.Type() {
	case dbft.PrepareRequestType:
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
	case dbft.PrepareResponseType:
		m.PreparationPayloads = append(m.PreparationPayloads, &preparationCompact{
			ValidatorIndex:   validator,
			InvocationScript: p.(*Payload).Witness,
		})

		if m.PreparationHashExt == nil {
			h := p.GetPrepareResponse().PreparationHash()
			m.PreparationHashExt = &h
		}
	case dbft.ChangeViewType:
		m.ChangeViewPayloads = append(m.ChangeViewPayloads, &changeViewCompact{
			ValidatorIndex:     validator,
			OriginalViewNumber: p.ViewNumber(),
			Timestamp:          p.GetChangeView().(*changeView).TimestampExt,
			InvocationScript:   p.(*Payload).Witness,
		})
	case dbft.PreCommitType:
		m.PreCommitPayloads = append(m.PreCommitPayloads, &preCommitCompact{
			ValidatorIndex:   validator,
			ViewNumber:       p.ViewNumber(),
			Data:             p.GetPreCommit().(*preCommit).dataExt,
			InvocationScript: p.(*Payload).Witness,
		})
	case dbft.CommitType:
		m.CommitPayloads = append(m.CommitPayloads, &commitCompact{
			ValidatorIndex:   validator,
			ViewNumber:       p.ViewNumber(),
			Signature:        p.GetCommit().(*commit).SignatureExt,
			InvocationScript: p.(*Payload).Witness,
		})
	}
}

// GetPrepareRequest implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetPrepareRequest(p dbft.ConsensusPayload[common.Hash], validators []dbft.PublicKey, primary uint16) dbft.ConsensusPayload[common.Hash] {
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
func (m *recoveryMessage) GetPrepareResponses(p dbft.ConsensusPayload[common.Hash], validators []dbft.PublicKey) []dbft.ConsensusPayload[common.Hash] {
	if m.PreparationHashExt == nil {
		return nil
	}

	ps := make([]dbft.ConsensusPayload[common.Hash], len(m.PreparationPayloads))

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
func (m *recoveryMessage) GetChangeViews(p dbft.ConsensusPayload[common.Hash], validators []dbft.PublicKey) []dbft.ConsensusPayload[common.Hash] {
	ps := make([]dbft.ConsensusPayload[common.Hash], len(m.ChangeViewPayloads))

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

// GetPreCommits implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetPreCommits(p dbft.ConsensusPayload[common.Hash], validators []dbft.PublicKey) []dbft.ConsensusPayload[common.Hash] {
	ps := make([]dbft.ConsensusPayload[common.Hash], len(m.PreCommitPayloads))

	for i, c := range m.PreCommitPayloads {
		cc := fromPayload(preCommitType, p.(*Payload), &preCommit{dataExt: c.Data})
		cc.SetValidatorIndex(uint16(c.ValidatorIndex))
		cc.Sender = validators[c.ValidatorIndex].(*PublicKey).Account
		cc.Witness = c.InvocationScript

		ps[i] = cc
	}

	return ps
}

// GetCommits implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) GetCommits(p dbft.ConsensusPayload[common.Hash], validators []dbft.PublicKey) []dbft.ConsensusPayload[common.Hash] {
	ps := make([]dbft.ConsensusPayload[common.Hash], len(m.CommitPayloads))

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
func (m *recoveryMessage) PreparationHash() *common.Hash {
	return m.PreparationHashExt
}

// SetPreparationHash implements the payload.RecoveryMessage interface.
func (m *recoveryMessage) SetPreparationHash(h *common.Hash) {
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
		PreCommitPayloads:   m.PreCommitPayloads,
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
	m.PreCommitPayloads = aux.PreCommitPayloads
	m.CommitPayloads = aux.CommitPayloads
	m.ChangeViewPayloads = aux.ChangeViewPayloads
	m.PreparationHashExt = aux.PreparationHashExt
	m.PrepareRequest = aux.PrepareRequest
	return nil
}
