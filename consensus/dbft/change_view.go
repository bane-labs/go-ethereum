package dbft

import (
	"github.com/nspcc-dev/dbft"
)

// changeView represents dBFT ChangeView message.
type changeView struct {
	// newViewNumber is not marshaled to rlp
	newViewNumber byte
	// TimestampExt is nanoseconds-precision payload TimestampExt, exactly like the one
	// that dBFT library operates internally with.
	TimestampExt uint64
	ReasonExt    dbft.ChangeViewReason
}

var _ dbft.ChangeView = (*changeView)(nil)

// NewViewNumber implements the payload.ChangeView interface.
func (c changeView) NewViewNumber() byte { return c.newViewNumber }

// Reason implements the payload.ChangeView interface.
func (c changeView) Reason() dbft.ChangeViewReason { return c.ReasonExt }
