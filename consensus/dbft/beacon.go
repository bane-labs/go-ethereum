package dbft

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Beacon is a CL API abstraction needed for proper dBFT work.
type Beacon interface {
	RefreshPendingPayload() error
	BlockBroadcaster() chan<- *types.Block
	SubscribeSyncingEvents(ch chan<- bool) event.Subscription
	SubscribeTransactionEvents(ch chan<- *types.Transaction) event.Subscription
}
