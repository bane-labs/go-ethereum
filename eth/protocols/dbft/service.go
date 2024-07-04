// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
)

// defaultSenderPoolSize is taken from NeoGo and roughly corresponds to 140
// items from 7 different senders. Usually it's enough.
const defaultSenderPoolSize = 20

// BlockChainAPI is enough of BlockChainAPI to satisfy [Service].
type BlockChainAPI interface {
	BlockNumber() hexutil.Uint64
}

// Service is the dbft network layer service.
type Service struct {
	bc BlockChainAPI

	lock  sync.RWMutex
	peers map[enode.ID]*Peer

	pool *Pool

	onPayload func(message *Message) error
}

// New creates a new instance of [Service].
func New(bc BlockChainAPI, onPayload func(*Message) error, isExtensibleAllowed func(common.Address) bool) *Service {
	poolLedger := newLedger(bc, isExtensibleAllowed)
	return &Service{
		bc:        bc,
		onPayload: onPayload,
		peers:     make(map[enode.ID]*Peer),
		pool:      NewPool(poolLedger, defaultSenderPoolSize),
	}
}

// MakeProtocols constructs the P2P protocol definitions for [Name].
func (s *Service) MakeProtocols() []p2p.Protocol {
	return []p2p.Protocol{{
		Name:       ProtocolName,
		Version:    0,
		Length:     numOfMsgs,
		Run:        s.Run,
		Attributes: []enr.Entry{},
	}}
}

// Run is the peer-handling callback for p2p layer.
func (s *Service) Run(p *p2p.Peer, rw p2p.MsgReadWriter) error {
	var peer = NewPeer(p, rw, defaultSenderPoolSize*2)

	s.lock.Lock()
	s.peers[p.ID()] = peer
	s.lock.Unlock()

	defer func() {
		s.lock.Lock()
		delete(s.peers, p.ID())
		s.lock.Unlock()
		peer.stop()
	}()

	return s.Handle(peer)
}

// BroadcastMessage adds a dbft message to the local pool and sends an
// announcement to all peers.
func (s *Service) BroadcastMessage(m *Message) error {
	new, err := s.pool.Add(m)
	if err != nil || !new {
		return err
	}
	var peers = s.grabPeers()
	h := m.Hash()
	for _, p := range peers {
		err := p.SendAnnounceMsg(h)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) grabPeers() []*Peer {
	s.lock.RLock()
	defer s.lock.RUnlock()

	var res = make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		res = append(res, p)
	}
	return res
}

func (s *Service) numOfPeers() int {
	s.lock.Lock()
	defer s.lock.Unlock()
	return len(s.peers)
}
