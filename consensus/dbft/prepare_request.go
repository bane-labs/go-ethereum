package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft/payload"
	"github.com/nspcc-dev/neo-go/pkg/util"
)

// prepareRequest represents dBFT prepareRequest message.
type prepareRequest struct {
	SealingProposal *types.Header
	OnPersist       *types.Transaction
	// TODO: remove OnPersist hash from TxHashes and adjust TransactionsHashes and SetTransactionsHashes wrt OnPersist.
	TxHashes []util.Uint256

	// Fields that should be included into PrepareRequest for its verification:
	ParentSealHash common.Hash
	ParentExtra    []byte
}

var _ payload.PrepareRequest = (*prepareRequest)(nil)

// Version implements the payload.PrepareRequest interface.
func (p prepareRequest) Version() uint32 {
	return 0
}

// SetVersion implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetVersion(v uint32) {
	return
}

// PrevHash implements the payload.PrepareRequest interface.
func (p prepareRequest) PrevHash() util.Uint256 {
	return p.SealingProposal.ParentHash.Uint256()
}

// SetPrevHash implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetPrevHash(h util.Uint256) {
	// No setter, the proposal must be kept as is.
	return
}

// Timestamp implements the payload.PrepareRequest interface.
func (p *prepareRequest) Timestamp() uint64 { return p.SealingProposal.Time * NsInS }

// SetTimestamp implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetTimestamp(ts uint64) {}

// Nonce implements the payload.PrepareRequest interface.
func (p *prepareRequest) Nonce() uint64 { return 0 }

// SetNonce implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetNonce(nonce uint64) {}

// TransactionHashes implements the payload.PrepareRequest interface.
func (p *prepareRequest) TransactionHashes() []util.Uint256 { return p.TxHashes }

// SetTransactionHashes implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetTransactionHashes(hs []util.Uint256) { p.TxHashes = hs }

// NextConsensus implements the payload.PrepareRequest interface.
func (p *prepareRequest) NextConsensus() util.Uint160 { return util.Uint160{} }

// SetNextConsensus implements the payload.PrepareRequest interface.
func (p *prepareRequest) SetNextConsensus(_ util.Uint160) {}
