package dbft

import (
	"io"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft"
)

// prepareRequest represents dBFT prepareRequest message.
type prepareRequest struct {
	SealingProposal *types.Header

	// extended denotes whether prepareRequest holds the whole set of transactions
	// instead of the hashes only.
	extended bool
	TxHashes []common.Hash
	Txs      []*Transaction

	// Fields that should be included into PrepareRequest for its verification for
	// pre-NeoXAMEV fork. Starting from NeoXAMEV+1 height these fields are filled
	// only if multisignature signing scheme is enforced.
	ParentSealHashV0 common.Hash
	ParentExtra      []byte
}

var _ dbft.PrepareRequest[common.Hash] = (*prepareRequest)(nil)

// Timestamp implements the payload.PrepareRequest interface.
func (p *prepareRequest) Timestamp() uint64 { return p.SealingProposal.Time * NsInS }

// Nonce implements the payload.PrepareRequest interface.
func (p *prepareRequest) Nonce() uint64 { return 0 }

// TransactionHashes implements the payload.PrepareRequest interface.
func (p *prepareRequest) TransactionHashes() []common.Hash {
	if p.extended {
		panic("bug: should not be called on PrepareRequestV0")
	}
	return p.TxHashes
}

// Transactions implements the payload.PrepareRequest interface.
func (p *prepareRequest) Transactions() []dbft.Transaction[common.Hash] {
	if !p.extended {
		panic("bug: should not be called on PrepareRequestV1")
	}
	res := make([]dbft.Transaction[common.Hash], len(p.Txs))
	for i, tx := range p.Txs {
		res[i] = tx
	}
	return res
}

// prepareRequestV0Aux represents an auxiluary structure for RLP prepareRequest
// marshalling. It holds dBFT prepareRequest message with shortened
// (hashes only) transaction info.
type prepareRequestV0Aux struct {
	SealingProposal *types.Header
	TxHashes        []common.Hash

	// Fields that should be included into PrepareRequest for its verification for
	// pre-NeoXAMEV fork. Starting from NeoXAMEV+1 height these fields are filled
	// only if multisignature signing scheme is enforced, hence marked as optional
	// for RLP serialization.
	ParentSealHashV0 common.Hash `rlp:"optional"`
	ParentExtra      []byte      `rlp:"optional"`
}

// prepareRequestV1Aux represents an auxiluary structure for RLP prepareRequest
// marshalling. It holds dBFT prepareRequest message with extended (full list of
// transactions) transaction info.
type prepareRequestV1Aux struct {
	SealingProposal *types.Header
	Txs             []*Transaction

	// Fields that should be included into PrepareRequest for its verification for
	// pre-NeoXAMEV fork. Starting from NeoXAMEV+1 height these fields are filled
	// only if multisignature signing scheme is enforced, hence marked as optional
	// for RLP serialization.
	ParentSealHashV0 common.Hash `rlp:"optional"`
	ParentExtra      []byte      `rlp:"optional"`
}

// DecodeRLP decodes prepareRequest from RLP.
func (m *prepareRequest) DecodeRLP(s *rlp.Stream) error {
	if m.extended {
		var aux prepareRequestV1Aux
		if err := s.Decode(&aux); err != nil {
			return err
		}
		m.SealingProposal = aux.SealingProposal
		m.Txs = aux.Txs
		m.ParentSealHashV0 = aux.ParentSealHashV0
		m.ParentExtra = aux.ParentExtra
	} else {
		var aux prepareRequestV0Aux
		if err := s.Decode(&aux); err != nil {
			return err
		}
		m.SealingProposal = aux.SealingProposal
		m.TxHashes = aux.TxHashes
		m.ParentSealHashV0 = aux.ParentSealHashV0
		m.ParentExtra = aux.ParentExtra
	}
	return nil
}

// EncodeRLP serializes prepareRequest as RLP.
func (m *prepareRequest) EncodeRLP(w io.Writer) error {
	if m.extended {
		return rlp.Encode(w, &prepareRequestV1Aux{
			SealingProposal:  m.SealingProposal,
			Txs:              m.Txs,
			ParentSealHashV0: m.ParentSealHashV0,
			ParentExtra:      m.ParentExtra,
		})
	}
	return rlp.Encode(w, &prepareRequestV0Aux{
		SealingProposal:  m.SealingProposal,
		TxHashes:         m.TxHashes,
		ParentSealHashV0: m.ParentSealHashV0,
		ParentExtra:      m.ParentExtra,
	})
}
