package dbft

import (
	"github.com/nspcc-dev/dbft/payload"
	"github.com/nspcc-dev/neo-go/pkg/util"
)

// prepareResponse represents dBFT PrepareResponse message.
type prepareResponse struct {
	PreparationHashExt util.Uint256
}

var _ payload.PrepareResponse = (*prepareResponse)(nil)

// PreparationHash implements the payload.PrepareResponse interface.
func (p *prepareResponse) PreparationHash() util.Uint256 { return p.PreparationHashExt }

// SetPreparationHash implements the payload.PrepareResponse interface.
func (p *prepareResponse) SetPreparationHash(h util.Uint256) { p.PreparationHashExt = h }
