package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft"
)

// prepareRequest represents dBFT prepareRequest message.
type prepareRequest struct {
	// TODO: after anti-MEV extension addition we need to remove useless fields from
	// PrepareRequest. Useless fields are those fields that depend on the final Block
	// state:
	//  - ReceiptHash
	//  - MixDigest
	//  - Root
	// These fields can't be verified by CNs at PrepareRequest level with AMEV logic.
	SealingProposal *types.Header
	TxHashes        []common.Hash

	// Fields that should be included into PrepareRequest for its verification:
	ParentSealHash common.Hash
	ParentExtra    []byte
}

var _ dbft.PrepareRequest[common.Hash] = (*prepareRequest)(nil)

// Timestamp implements the payload.PrepareRequest interface.
func (p *prepareRequest) Timestamp() uint64 { return p.SealingProposal.Time * NsInS }

// Nonce implements the payload.PrepareRequest interface.
func (p *prepareRequest) Nonce() uint64 { return 0 }

// TransactionHashes implements the payload.PrepareRequest interface.
func (p *prepareRequest) TransactionHashes() []common.Hash { return p.TxHashes }
