package dbft

import (
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/event"
)

// ChainHeaderReader is a Blockchain API abstraction needed for proper dBFT work.
type ChainHeaderReader interface {
	consensus.ChainHeaderReader
	SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
}
