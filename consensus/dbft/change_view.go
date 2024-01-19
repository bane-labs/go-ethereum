package dbft

import (
	"github.com/nspcc-dev/dbft/payload"
)

// changeView represents dBFT ChangeView message.
type changeView struct {
	// newViewNumber is not marshaled to rlp
	newViewNumber byte
	// TimestampExt is nanoseconds-precision payload TimestampExt, exactly like the one
	// that dBFT library operates internally with.
	TimestampExt uint64
	ReasonExt    payload.ChangeViewReason
}

var _ payload.ChangeView = (*changeView)(nil)

// NewViewNumber implements the payload.ChangeView interface.
func (c changeView) NewViewNumber() byte { return c.newViewNumber }

// SetNewViewNumber implements the payload.ChangeView interface.
func (c *changeView) SetNewViewNumber(view byte) { c.newViewNumber = view }

// Timestamp implements the payload.ChangeView interface.
func (c changeView) Timestamp() uint64 { return c.TimestampExt }

// SetTimestamp implements the payload.ChangeView interface.
func (c *changeView) SetTimestamp(ts uint64) { c.TimestampExt = ts }

// Reason implements the payload.ChangeView interface.
func (c changeView) Reason() payload.ChangeViewReason { return c.ReasonExt }

// SetReason implements the payload.ChangeView interface.
func (c *changeView) SetReason(reason payload.ChangeViewReason) { c.ReasonExt = reason }
