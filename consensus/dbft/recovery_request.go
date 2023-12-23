package dbft

import (
	"github.com/nspcc-dev/dbft/payload"
)

// recoveryRequest represents dBFT RecoveryRequest message.
type recoveryRequest struct {
	TimestampExt uint64
}

var _ payload.RecoveryRequest = (*recoveryRequest)(nil)

// Timestamp implements the payload.RecoveryRequest interface.
func (m *recoveryRequest) Timestamp() uint64 { return m.TimestampExt }

// SetTimestamp implements the payload.RecoveryRequest interface.
func (m *recoveryRequest) SetTimestamp(ts uint64) { m.TimestampExt = ts }
