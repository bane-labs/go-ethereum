package eth

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/beacon"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// testBeaconHandler is a mock event handler to listen for inbound network requests
// on the `beacon` protocol and convert them into a more easily testable form.
type testBeaconHandler struct {
	blockBroadcasts event.Feed
}

func (h *testBeaconHandler) Chain() *core.BlockChain                    { panic("no backing chain") }
func (h *testBeaconHandler) RunPeer(*beacon.Peer, beacon.Handler) error { panic("not used in tests") }
func (h *testBeaconHandler) PeerInfo(enode.ID) interface{}              { panic("not used in tests") }

func (h *testBeaconHandler) Handle(peer *beacon.Peer, packet beacon.Packet) error {
	switch packet := packet.(type) {
	case *beacon.NewBlockPacket:
		h.blockBroadcasts.Send(packet.Block)
		return nil

	default:
		panic(fmt.Sprintf("unexpected beacon packet type in tests: %T", packet))
	}
}

// Tests that blocks are broadcast to a sqrt number of peers only.
func TestBroadcastBlock1Peer(t *testing.T)    { testBroadcastBlock(t, 1, 1) }
func TestBroadcastBlock2Peers(t *testing.T)   { testBroadcastBlock(t, 2, 1) }
func TestBroadcastBlock3Peers(t *testing.T)   { testBroadcastBlock(t, 3, 1) }
func TestBroadcastBlock4Peers(t *testing.T)   { testBroadcastBlock(t, 4, 2) }
func TestBroadcastBlock5Peers(t *testing.T)   { testBroadcastBlock(t, 5, 2) }
func TestBroadcastBlock8Peers(t *testing.T)   { testBroadcastBlock(t, 9, 3) }
func TestBroadcastBlock12Peers(t *testing.T)  { testBroadcastBlock(t, 12, 3) }
func TestBroadcastBlock16Peers(t *testing.T)  { testBroadcastBlock(t, 16, 4) }
func TestBroadcastBloc26Peers(t *testing.T)   { testBroadcastBlock(t, 26, 5) }
func TestBroadcastBlock100Peers(t *testing.T) { testBroadcastBlock(t, 100, 10) }

func testBroadcastBlock(t *testing.T, peers, bcasts int) {
	t.Parallel()

	// Create a source handler to broadcast blocks from and a number of sinks
	// to receive them.
	source := newTestHandlerWithBlocks(1)
	defer source.close()

	sinks := make([]*testBeaconHandler, peers)
	for i := 0; i < len(sinks); i++ {
		sinks[i] = new(testBeaconHandler)
	}
	// Interconnect all the sink handlers with the source handler
	var (
		genesis = source.chain.Genesis()
		td      = source.chain.GetTd(genesis.Hash(), genesis.NumberU64())
	)
	for i, sink := range sinks {
		sink := sink // Closure for gorotuine below

		sourcePipe, sinkPipe := p2p.MsgPipe()
		defer sourcePipe.Close()
		defer sinkPipe.Close()

		sourcePeer := beacon.NewPeer(beacon.BEACON1, p2p.NewPeerPipe(enode.ID{byte(i)}, "", nil, sourcePipe), sourcePipe)
		sinkPeer := beacon.NewPeer(beacon.BEACON1, p2p.NewPeerPipe(enode.ID{0}, "", nil, sinkPipe), sinkPipe)
		defer sourcePeer.Close()
		defer sinkPeer.Close()

		go source.handler.runBeaconPeer(sourcePeer, func(peer *beacon.Peer) error {
			return beacon.Handle((*beaconHandler)(source.handler), peer)
		})
		if err := sinkPeer.Handshake(1, td, genesis.Hash(), genesis.Hash(), forkid.NewIDWithChain(source.chain), forkid.NewFilter(source.chain)); err != nil {
			t.Fatalf("failed to run protocol handshake")
		}
		go beacon.Handle(sink, sinkPeer)
	}
	// Subscribe to all the block channels
	blockChs := make([]chan *types.Block, len(sinks))
	for i := 0; i < len(sinks); i++ {
		blockChs[i] = make(chan *types.Block, 1)
		defer close(blockChs[i])

		sub := sinks[i].blockBroadcasts.Subscribe(blockChs[i])
		defer sub.Unsubscribe()
	}
	// Initiate a block propagation across the peers
	time.Sleep(100 * time.Millisecond)
	header := source.chain.CurrentBlock()
	source.handler.BroadcastBlock(source.chain.GetBlock(header.Hash(), header.Number.Uint64()), true)

	// Iterate through all the sinks and ensure the correct number got the block
	done := make(chan struct{}, peers)
	for _, ch := range blockChs {
		ch := ch
		go func() {
			<-ch
			done <- struct{}{}
		}()
	}
	var received int
	for {
		select {
		case <-done:
			received++

		case <-time.After(100 * time.Millisecond):
			if received != bcasts {
				t.Errorf("broadcast count mismatch: have %d, want %d", received, bcasts)
			}
			return
		}
	}
}

// Tests that a propagated malformed block (uncles or transactions don't match
// with the hashes in the header) gets discarded and not broadcast forward.
func TestBroadcastMalformedBlock1(t *testing.T) { testBroadcastMalformedBlock(t, beacon.BEACON1) }

func testBroadcastMalformedBlock(t *testing.T, protocol uint) {
	t.Parallel()

	// Create a source handler to broadcast blocks from and a number of sinks
	// to receive them.
	source := newTestHandlerWithBlocks(1)
	defer source.close()

	// Create a source handler to send messages through and a sink peer to receive them
	p2pSrc, p2pSink := p2p.MsgPipe()
	defer p2pSrc.Close()
	defer p2pSink.Close()

	src := beacon.NewPeer(protocol, p2p.NewPeerPipe(enode.ID{1}, "", nil, p2pSrc), p2pSrc)
	sink := beacon.NewPeer(protocol, p2p.NewPeerPipe(enode.ID{2}, "", nil, p2pSink), p2pSink)
	defer src.Close()
	defer sink.Close()

	go source.handler.runBeaconPeer(src, func(peer *beacon.Peer) error {
		return beacon.Handle((*beaconHandler)(source.handler), peer)
	})
	// Run the handshake locally to avoid spinning up a sink handler
	var (
		genesis = source.chain.Genesis()
		td      = source.chain.GetTd(genesis.Hash(), genesis.NumberU64())
	)
	if err := sink.Handshake(1, td, genesis.Hash(), genesis.Hash(), forkid.NewIDWithChain(source.chain), forkid.NewFilter(source.chain)); err != nil {
		t.Fatalf("failed to run protocol handshake")
	}
	// After the handshake completes, the source handler should stream the sink
	// the blocks, subscribe to inbound network events
	backend := new(testBeaconHandler)

	blocks := make(chan *types.Block, 1)
	sub := backend.blockBroadcasts.Subscribe(blocks)
	defer sub.Unsubscribe()

	go beacon.Handle(backend, sink)

	// Create various combinations of malformed blocks
	head := source.chain.CurrentBlock()
	block := source.chain.GetBlock(head.Hash(), head.Number.Uint64())

	malformedUncles := head
	malformedUncles.UncleHash[0]++
	malformedTransactions := head
	malformedTransactions.TxHash[0]++
	malformedEverything := head
	malformedEverything.UncleHash[0]++
	malformedEverything.TxHash[0]++

	// Try to broadcast all malformations and ensure they all get discarded
	for _, header := range []*types.Header{malformedUncles, malformedTransactions, malformedEverything} {
		block := types.NewBlockWithHeader(header).WithBody(types.Body{Transactions: block.Transactions(), Uncles: block.Uncles()})
		if err := src.SendNewBlock(block, big.NewInt(131136)); err != nil {
			t.Fatalf("failed to broadcast block: %v", err)
		}
		select {
		case <-blocks:
			t.Fatalf("malformed block forwarded")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
