package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft"
)

// prepareRequest represents dBFT prepareRequest message.
type prepareRequest struct {
	SealingProposal *types.Header
	TxHashes        []common.Hash

	// Fields that should be included into PrepareRequest for its verification for
	// pre-NeoXAMEV fork. Starting from NeoXAMEV+1 height these fields are filled
	// only if multisignature signing scheme is enforced, hence marked as optional
	// for RLP serialization.
	ParentSealHash common.Hash `rlp:optional`
	ParentExtra    []byte      `rlp:"optional"`
}

var _ dbft.PrepareRequest[common.Hash] = (*prepareRequest)(nil)

// Timestamp implements the payload.PrepareRequest interface.
func (p *prepareRequest) Timestamp() uint64 { return p.SealingProposal.Time * NsInS }

// Nonce implements the payload.PrepareRequest interface.
func (p *prepareRequest) Nonce() uint64 { return 0 }

// TransactionHashes implements the payload.PrepareRequest interface.
func (p *prepareRequest) TransactionHashes() []common.Hash { return p.TxHashes }
