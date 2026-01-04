package beacon

import (
	"time"

	"github.com/ethereum/go-ethereum/p2p/tracker"
)

// requestTracker is a singleton tracker for request times.
var requestTracker = tracker.New(ProtocolName, 5*time.Minute)
