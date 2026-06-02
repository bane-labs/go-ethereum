package beacon

import (
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/p2p"
)

const (
	// handshakeTimeout is the maximum allowed time for the `beacon` handshake to
	// complete before dropping the connection as malicious.
	handshakeTimeout = 5 * time.Second
)

// Handshake executes the eth protocol handshake, negotiating version number,
// network IDs, difficulties, head and genesis blocks.
func (p *Peer) Handshake(network uint64, chain *core.BlockChain, blobSync bool) error {
	switch p.version {
	case BEACON2:
		return p.handshake2(network, chain, blobSync)
	case BEACON1:
		return p.handshake1(network, chain, blobSync)
	default:
		return errors.New("unsupported protocol version")
	}
}

func (p *Peer) handshake1(network uint64, chain *core.BlockChain, blobSync bool) error {
	var (
		genesis    = chain.Genesis()
		latest     = chain.CurrentBlock()
		forkID     = forkid.NewID(chain.Config(), genesis, latest.Number.Uint64(), latest.Time)
		forkFilter = forkid.NewFilter(chain)
		td         = chain.GetTd(latest.Hash(), latest.Number.Uint64())
	)
	// Send out own handshake in a new thread
	errc := make(chan error, 2)

	var status StatusPacket1 // safe to read after two values have been received from errc

	go func() {
		errc <- p2p.Send(p.rw, StatusMsg, &StatusPacket1{
			ProtocolVersion: uint32(p.version),
			NetworkID:       network,
			TD:              td,
			Head:            latest.Hash(),
			Genesis:         genesis.Hash(),
			ForkID:          forkID,
			BlobSync:        blobSync,
		})
	}()
	go func() {
		errc <- p.readStatus1(network, &status, genesis.Hash(), forkFilter)
	}()

	return waitForHandshake(errc, p)
}

// readStatus1 reads the remote handshake message.
func (p *Peer) readStatus1(network uint64, status *StatusPacket1, genesis common.Hash, forkFilter forkid.Filter) error {
	msg, err := p.rw.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Code != StatusMsg {
		return fmt.Errorf("%w: first msg has code %x (!= %x)", errNoStatusMsg, msg.Code, StatusMsg)
	}
	if msg.Size > maxMessageSize {
		return fmt.Errorf("%w: %v > %v", errMsgTooLarge, msg.Size, maxMessageSize)
	}
	// Decode the handshake and make sure everything matches
	if err := msg.Decode(&status); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	if status.NetworkID != network {
		return fmt.Errorf("%w: %d (!= %d)", errNetworkIDMismatch, status.NetworkID, network)
	}
	if uint(status.ProtocolVersion) != p.version {
		return fmt.Errorf("%w: %d (!= %d)", errProtocolVersionMismatch, status.ProtocolVersion, p.version)
	}
	if status.Genesis != genesis {
		return fmt.Errorf("%w: %x (!= %x)", errGenesisMismatch, status.Genesis, genesis)
	}
	if err := forkFilter(status.ForkID); err != nil {
		return fmt.Errorf("%w: %v", errForkIDRejected, err)
	}
	p.td, p.head = status.TD, status.Head
	p.blobSync = status.BlobSync
	return nil
}

func (p *Peer) handshake2(network uint64, chain *core.BlockChain, blobSync bool) error {
	var (
		genesis    = chain.Genesis()
		latest     = chain.CurrentBlock()
		forkID     = forkid.NewID(chain.Config(), genesis, latest.Number.Uint64(), latest.Time)
		td         = chain.GetTd(latest.Hash(), latest.Number.Uint64())
		forkFilter = forkid.NewFilter(chain)
	)
	// Send out own handshake in a new thread
	errc := make(chan error, 2)

	var status StatusPacket2 // safe to read after two values have been received from errc

	go func() {
		errc <- p2p.Send(p.rw, StatusMsg, &StatusPacket2{
			ProtocolVersion: uint32(p.version),
			NetworkID:       network,
			TD:              td,
			Head:            latest.Hash(),
			HeadNumber:      latest.Number.Uint64(),
			Genesis:         genesis.Hash(),
			ForkID:          forkID,
			BlobSync:        blobSync,
		})
	}()
	go func() {
		errc <- p.readStatus2(network, &status, genesis.Hash(), forkFilter)
	}()

	return waitForHandshake(errc, p)
}

// readStatus1 reads the remote handshake message.
func (p *Peer) readStatus2(network uint64, status *StatusPacket2, genesis common.Hash, forkFilter forkid.Filter) error {
	msg, err := p.rw.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Code != StatusMsg {
		return fmt.Errorf("%w: first msg has code %x (!= %x)", errNoStatusMsg, msg.Code, StatusMsg)
	}
	if msg.Size > maxMessageSize {
		return fmt.Errorf("%w: %v > %v", errMsgTooLarge, msg.Size, maxMessageSize)
	}
	// Decode the handshake and make sure everything matches
	if err := msg.Decode(&status); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	if status.NetworkID != network {
		return fmt.Errorf("%w: %d (!= %d)", errNetworkIDMismatch, status.NetworkID, network)
	}
	if uint(status.ProtocolVersion) != p.version {
		return fmt.Errorf("%w: %d (!= %d)", errProtocolVersionMismatch, status.ProtocolVersion, p.version)
	}
	if status.Genesis != genesis {
		return fmt.Errorf("%w: %x (!= %x)", errGenesisMismatch, status.Genesis, genesis)
	}
	if err := forkFilter(status.ForkID); err != nil {
		return fmt.Errorf("%w: %v", errForkIDRejected, err)
	}
	p.td, p.head, p.headNumber = status.TD, status.Head, status.HeadNumber
	p.blobSync = status.BlobSync
	return nil
}

func waitForHandshake(errc <-chan error, p *Peer) error {
	timeout := time.NewTimer(handshakeTimeout)
	defer timeout.Stop()
	for range 2 {
		select {
		case err := <-errc:
			if err != nil {
				markError(p, err)
				return err
			}
		case <-timeout.C:
			markError(p, p2p.DiscReadTimeout)
			return p2p.DiscReadTimeout
		}
	}
	return nil
}

// markError registers the error with the corresponding metric.
func markError(p *Peer, err error) {
	if !metrics.Enabled() {
		return
	}
	m := meters.get(p.Inbound())
	switch errors.Unwrap(err) {
	case errNetworkIDMismatch:
		m.networkIDMismatch.Mark(1)
	case errProtocolVersionMismatch:
		m.protocolVersionMismatch.Mark(1)
	case errGenesisMismatch:
		m.genesisMismatch.Mark(1)
	case errForkIDRejected:
		m.forkidRejected.Mark(1)
	case p2p.DiscReadTimeout:
		m.timeoutError.Mark(1)
	default:
		m.peerError.Mark(1)
	}
}
