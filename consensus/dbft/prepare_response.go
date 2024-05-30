package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/nspcc-dev/dbft"
)

// prepareResponse represents dBFT PrepareResponse message.
type prepareResponse struct {
	PreparationHashExt common.Hash
}

var _ dbft.PrepareResponse[common.Hash] = (*prepareResponse)(nil)

// PreparationHash implements the payload.PrepareResponse interface.
func (p *prepareResponse) PreparationHash() common.Hash { return p.PreparationHashExt }
