package dbft

import (
	"github.com/nspcc-dev/dbft/payload"
)

// commit represents dBFT Commit message.
type commit struct {
	SignatureExt [extraSeal]byte
}

var _ payload.Commit = (*commit)(nil)

// Signature implements the payload.Commit interface.
func (c commit) Signature() []byte { return c.SignatureExt[:] }

// SetSignature implements the payload.Commit interface.
func (c *commit) SetSignature(signature []byte) {
	copy(c.SignatureExt[:], signature)
}
