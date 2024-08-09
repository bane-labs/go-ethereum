// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/stretchr/testify/require"
)

type testBC struct {
	height hexutil.Uint64
}

func (t *testBC) BlockNumber() hexutil.Uint64 {
	return t.height
}

func TestHandling(t *testing.T) {
	var (
		key, _         = crypto.GenerateKey()
		addr           = crypto.PubkeyToAddress(key.PublicKey)
		bc             = &testBC{height: 10}
		s1             = New(bc, nil, func(u uint64, address common.Address) bool { return true })
		s2             = New(bc, nil, func(u uint64, address common.Address) bool { return true })
		s3             = New(bc, nil, func(u uint64, address common.Address) bool { return true })
		p1             = p2p.NewPeer(enode.ID{1}, "peer1", nil)
		p2             = p2p.NewPeer(enode.ID{2}, "peer2", nil)
		p3             = p2p.NewPeer(enode.ID{3}, "peer3", nil)
		pipe12, pipe21 = p2p.MsgPipe()
		pipe23, pipe32 = p2p.MsgPipe()
	)
	go s1.Run(p2, pipe12)
	go s2.Run(p1, pipe21)
	go s2.Run(p3, pipe23)
	go s3.Run(p2, pipe32)

	// Wait for peers to register.
	require.Eventually(t, func() bool {
		return s1.numOfPeers() == 1
	}, time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return s2.numOfPeers() == 2
	}, time.Second, 10*time.Millisecond)

	// Broadcast.
	m := someMessage(t, 11, addr, key)
	s1.BroadcastMessage(m)

	// S2 gets it first right from s1 (CN).
	require.Eventually(t, func() bool {
		return s2.pool.Get(m.Hash()) != nil
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, m, s2.pool.Get(m.Hash()))

	// S3 gets it from S2 (intermediary).
	require.Eventually(t, func() bool {
		return s3.pool.Get(m.Hash()) != nil
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, m, s3.pool.Get(m.Hash()))
}
