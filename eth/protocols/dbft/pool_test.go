// Copyright 2023 NeoSPCC
//
// MIT License.
package dbft

import (
	"crypto/ecdsa"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestAddGet(t *testing.T) {
	bc := newTestChain()
	bc.height = 10

	p := NewPool(bc, 100)
	t.Run("invalid witness", func(t *testing.T) {
		m := someMessage(t, 100, bc.goodAddrs[0], bc.badKey)
		p.testAdd(t, false, invalidSig, m)
	})
	t.Run("syncing", func(t *testing.T) {
		bc.syncing = true
		m := bc.badMessage(t, 100)
		p.testAdd(t, false, nil, m)
		bc.syncing = false
	})
	t.Run("disallowed sender", func(t *testing.T) {
		m := bc.badMessage(t, 100)
		p.testAdd(t, false, errDisallowedSender, m)
	})
	t.Run("bad height", func(t *testing.T) {
		m := bc.goodMessage(t, 9)
		p.testAdd(t, false, errInvalidHeight, m)

		m = bc.goodMessage(t, 10)
		p.testAdd(t, false, nil, m)
	})
	t.Run("good", func(t *testing.T) {
		m := bc.goodMessage(t, 11)

		p.testAdd(t, true, nil, m)
		require.Equal(t, m, p.Get(m.Hash()))
		p.testAdd(t, false, nil, m)
	})
}

func TestCapacityLimit(t *testing.T) {
	bc := newTestChain()
	bc.height = 10

	t.Run("invalid capacity", func(t *testing.T) {
		require.Panics(t, func() { NewPool(bc, 0) })
	})

	p := NewPool(bc, 3)

	first := bc.goodMessage(t, 11)
	p.testAdd(t, true, nil, first)

	for _, height := range []uint64{12, 13} {
		m := bc.goodMessage(t, height)
		p.testAdd(t, true, nil, m)
	}

	require.NotNil(t, p.Get(first.Hash()))

	ok, err := p.Add(bc.goodMessage(t, 14))
	require.True(t, ok)
	require.NoError(t, err)

	require.Nil(t, p.Get(first.Hash()))
}

func TestRemoveStale(t *testing.T) {
	bc := newTestChain()
	bc.height = 10
	bc.goodAddrs = append(bc.goodAddrs, crypto.PubkeyToAddress(bc.badKey.PublicKey))

	p := NewPool(bc, 100)
	eps := []*Message{
		bc.goodMessage(t, 11), // small height
		bc.goodMessage(t, 12), // good
		bc.badMessage(t, 12),  // invalid sender
	}
	for i := range eps {
		p.testAdd(t, true, nil, eps[i])
	}
	bc.goodAddrs = bc.goodAddrs[:1] // drop bad key again
	p.RemoveStale(11)
	require.Nil(t, p.Get(eps[0].Hash()))
	require.Equal(t, eps[1], p.Get(eps[1].Hash()))
	require.Nil(t, p.Get(eps[2].Hash()))
}

func (p *Pool) testAdd(t *testing.T, expectedOk bool, expectedErr error, ep *Message) {
	ok, err := p.Add(ep)
	if expectedErr != nil {
		require.ErrorIs(t, err, expectedErr)
	} else {
		require.NoError(t, err)
	}
	require.Equal(t, expectedOk, ok)
}

type testChain struct {
	height    uint64
	goodKey   *ecdsa.PrivateKey
	badKey    *ecdsa.PrivateKey
	goodAddrs []common.Address
	badAddr   common.Address
	syncing   bool
}

var errVerification = errors.New("verification failed")

func newTestChain() *testChain {
	gk, _ := crypto.GenerateKey()
	bk, _ := crypto.GenerateKey()
	return &testChain{
		goodKey:   gk,
		badKey:    bk,
		goodAddrs: []common.Address{crypto.PubkeyToAddress(gk.PublicKey)},
		badAddr:   crypto.PubkeyToAddress(bk.PublicKey),
	}
}
func (c *testChain) IsAddressAllowed(u common.Address) error {
	if c.syncing {
		return ErrSyncing
	}
	for i := range c.goodAddrs {
		if u == c.goodAddrs[i] {
			return nil
		}
	}
	return errors.New("address not allowed")
}
func (c *testChain) BlockHeight() uint64 { return c.height }

func (c *testChain) goodMessage(t *testing.T, height uint64) *Message {
	return someMessage(t, height, c.goodAddrs[0], c.goodKey)
}

func (c *testChain) badMessage(t *testing.T, height uint64) *Message {
	return someMessage(t, height, c.badAddr, c.badKey)
}

func someMessage(t *testing.T, height uint64, sender common.Address, signer *ecdsa.PrivateKey) *Message {
	m := &Message{ValidBlockEnd: height, Sender: sender, Data: []byte{42}}
	h := m.Hash()
	sig, err := crypto.Sign(h[:], signer)
	require.NoError(t, err)
	m.Witness = sig

	return m
}
