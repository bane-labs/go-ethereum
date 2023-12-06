// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/rlp"
)

// Peer is a dbft peer.
type Peer struct {
	*p2p.Peer
	rw     p2p.MsgReadWriter
	stream chan p2p.Msg
	finish chan struct{}
	done   chan struct{}
}

// NewPeer creates an instance of [Peer].
func NewPeer(peer *p2p.Peer, rw p2p.MsgReadWriter, queueSize int) *Peer {
	var p = &Peer{
		Peer:   peer,
		rw:     rw,
		stream: make(chan p2p.Msg, queueSize),
		finish: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go p.sendLoop()
	return p
}

// SendAnnounceMsg sends an [AnnounceMsg] with the given hash.
func (p *Peer) SendAnnounceMsg(h common.Hash) error {
	return p.sendMsg(AnnounceMsg, h)
}

// SendGetMsg sends a [GetMsg] with the given hash.
func (p *Peer) SendGetMsg(h common.Hash) error {
	return p.sendMsg(GetMsg, h)
}

// SendMessage sends a [DBFTMsg] with the given payload.
func (p *Peer) SendMessage(m *Message) error {
	return p.sendMsg(DBFTMsg, m)
}

func (p *Peer) sendMsg(code uint64, val any) error {
	size, r, err := rlp.EncodeToReader(val)
	if err != nil {
		return err
	}
	var m = p2p.Msg{Code: code, Size: uint32(size), Payload: r}
	select {
	case p.stream <- m:
	case <-p.finish:
	}
	return nil
}

func (p *Peer) stop() {
	close(p.finish)
	<-p.done
}

func (p *Peer) sendLoop() {
sendloop:
	for {
		select {
		case <-p.finish:
			break sendloop
		case m := <-p.stream:
			err := p.rw.WriteMsg(m)
			if err != nil {
				p.Log().Debug("write error", err)
			}
		}
	}
drainloop:
	for {
		select {
		case <-p.stream:
		default:
			break drainloop
		}
	}
	close(p.done)
}
