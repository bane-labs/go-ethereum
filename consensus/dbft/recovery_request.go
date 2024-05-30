package dbft

import (
	"github.com/nspcc-dev/dbft"
)

// recoveryRequest represents dBFT RecoveryRequest message.
type recoveryRequest struct {
	TimestampExt uint64
}

var _ dbft.RecoveryRequest = (*recoveryRequest)(nil)

// Timestamp implements the payload.RecoveryRequest interface.
func (m *recoveryRequest) Timestamp() uint64 { return m.TimestampExt }
