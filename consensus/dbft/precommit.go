package dbft

import (
	"github.com/nspcc-dev/dbft"
)

// preCommit represents dBFT PreCommit message.
type preCommit struct {
	DataExt []byte
}

var _ dbft.PreCommit = (*preCommit)(nil)

// Data implements the payload.PreCommit interface.
func (p preCommit) Data() []byte { return p.DataExt }
