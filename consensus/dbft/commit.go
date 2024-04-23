package dbft

import (
	"github.com/nspcc-dev/dbft"
)

// commit represents dBFT Commit message.
type commit struct {
	SignatureExt [extraSeal]byte
}

var _ dbft.Commit = (*commit)(nil)

// Signature implements the payload.Commit interface.
func (c commit) Signature() []byte { return c.SignatureExt[:] }
