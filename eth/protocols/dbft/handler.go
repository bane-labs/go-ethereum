// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/p2p"
)

const (
	// ProtocolName is the dbft p2p protocol name.
	ProtocolName = "dbft"
)

// Message type definitions.
const (
	AnnounceMsg = 0x00
	GetMsg      = 0x01
	DBFTMsg     = 0x02

	numOfMsgs = 3
)

const (
	// maxMessageSize is twice 64K of 32 byte hashes, N3 uses 32M, while
	// Ethereum mostly uses 10M. Should be sufficient.
	maxMessageSize = 4 * 1024 * 1024
)

// ErrSyncing is returned when operation can't be performed due to the fact that
// the node is in the process of chain sync.
var ErrSyncing = errors.New("node is syncing")

var (
	errMsgTooLarge    = errors.New("message too long")
	errDecode         = errors.New("invalid message")
	errInvalidMsgCode = errors.New("invalid message code")
)

// Handle is the callback invoked to manage the life cycle of a dbft peer.
// When this function terminates, the peer is disconnected.
func (s *Service) Handle(peer *Peer) error {
	for {
		if err := s.HandleMessage(peer); err != nil {
			peer.Log().Debug("Message handling failed in `dbft`", "err", err)
			return err
		}
	}
}

// HandleMessage is invoked whenever an inbound message is received from a
// remote peer on the `snap` protocol. The remote connection is torn down upon
// returning any error.
func (s *Service) HandleMessage(peer *Peer) error {
	// Read the next message from the remote peer, and ensure it's fully consumed
	msg, err := peer.rw.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Size > maxMessageSize {
		return fmt.Errorf("%w: %v > %v", errMsgTooLarge, msg.Size, maxMessageSize)
	}
	defer msg.Discard()
	start := time.Now()
	// Track the amount of time it takes to serve the request and run the handler
	if metrics.Enabled() {
		h := fmt.Sprintf("%s/%s/%#02x", p2p.HandleHistName, ProtocolName, msg.Code)
		defer func(start time.Time) {
			sampler := func() metrics.Sample {
				return metrics.ResettingSample(
					metrics.NewExpDecaySample(1028, 0.015),
				)
			}
			metrics.GetOrRegisterHistogramLazy(h, nil, sampler).Update(time.Since(start).Microseconds())
		}(start)
	}
	// Handle the message depending on its contents
	switch {
	case msg.Code == AnnounceMsg:
		var h common.Hash
		if err := msg.Decode(&h); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
		if s.pool.Get(h) == nil {
			peer.SendGetMsg(h)
		}
	case msg.Code == GetMsg:
		var h common.Hash
		if err := msg.Decode(&h); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
		var m = s.pool.Get(h)
		if m != nil {
			return peer.SendMessage(m)
		}
	case msg.Code == DBFTMsg:
		var m Message
		if err := msg.Decode(&m); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
		if s.onPayload != nil {
			err := s.onPayload(&m)
			if err != nil {
				return fmt.Errorf("failed to handle dBFT message: %w", err)
			}
		}
		return s.BroadcastMessage(&m)
	default:
		return fmt.Errorf("%w: %v", errInvalidMsgCode, msg.Code)
	}
	return nil
}
