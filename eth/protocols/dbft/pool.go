// Copyright 2023 NeoSPCC
//
// MIT License.
package dbft

import (
	"container/list"
	"errors"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// Ledger is enough of Blockchain to satisfy Pool.
type Ledger interface {
	BlockHeight() uint64
	IsAddressAllowed(common.Address) error
}

// Pool represents a pool of extensible payloads.
type Pool struct {
	lock     sync.RWMutex
	verified map[common.Hash]*list.Element
	senders  map[common.Address]*list.List
	// singleCap represents the maximum number of payloads from a single sender.
	singleCap int
	chain     Ledger
}

// NewPool returns a new payload pool using the provided chain.
func NewPool(bc Ledger, capacity int) *Pool {
	if capacity <= 0 {
		panic("invalid capacity")
	}

	return &Pool{
		verified:  make(map[common.Hash]*list.Element),
		senders:   make(map[common.Address]*list.List),
		singleCap: capacity,
		chain:     bc,
	}
}

var (
	errDisallowedSender = errors.New("disallowed sender")
	errInvalidHeight    = errors.New("invalid height")
)

// Add adds an extensible payload to the pool.
// First return value specifies if the payload was new.
// Second one is nil if and only if the payload is valid.
func (p *Pool) Add(m *Message) (bool, error) {
	if ok, err := p.verify(m); err != nil || !ok {
		return ok, err
	}

	p.lock.Lock()
	defer p.lock.Unlock()

	h := m.Hash()
	if _, ok := p.verified[h]; ok {
		return false, nil
	}

	lst, ok := p.senders[m.Sender]
	if ok && lst.Len() >= p.singleCap {
		value := lst.Remove(lst.Front())
		delete(p.verified, value.(*Message).Hash())
	} else if !ok {
		lst = list.New()
		p.senders[m.Sender] = lst
	}

	p.verified[h] = lst.PushBack(m)
	return true, nil
}

func (p *Pool) verify(m *Message) (bool, error) {
	err := m.Verify()
	if err != nil {
		return false, err
	}

	h := p.chain.BlockHeight()
	if h < m.ValidBlockStart || m.ValidBlockEnd <= h {
		// We can receive a consensus payload for the last or next block
		// which leads to an unwanted node disconnect.
		if m.ValidBlockEnd == h {
			return false, nil
		}
		return false, errInvalidHeight
	}
	err = p.chain.IsAddressAllowed(m.Sender)
	if err != nil {
		// There's no reliable way to check sender for syncing node.
		if errors.Is(err, ErrSyncing) {
			return false, nil
		}
		return false, fmt.Errorf("%w: %w", errDisallowedSender, err)
	}
	return true, nil
}

// Get returns payload by hash.
func (p *Pool) Get(h common.Hash) *Message {
	p.lock.RLock()
	defer p.lock.RUnlock()

	elem, ok := p.verified[h]
	if !ok {
		return nil
	}
	return elem.Value.(*Message)
}

// RemoveStale removes invalid payloads after block processing.
func (p *Pool) RemoveStale(index uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	for s, lst := range p.senders {
		for elem := lst.Front(); elem != nil; {
			m := elem.Value.(*Message)
			h := m.Hash()
			old := elem
			elem = elem.Next()

			if m.ValidBlockEnd <= index || p.chain.IsAddressAllowed(m.Sender) != nil {
				delete(p.verified, h)
				lst.Remove(old)
				continue
			}
		}
		if lst.Len() == 0 {
			delete(p.senders, s)
		}
	}
}
