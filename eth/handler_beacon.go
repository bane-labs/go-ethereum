package eth

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth/protocols/beacon"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// beaconHandler implements the eth.Backend interface to handle the various network
// packets that are sent as replies or broadcasts.
type beaconHandler handler

func (h *beaconHandler) Chain() *core.BlockChain { return h.chain }

// RunPeer is invoked when a peer joins on the `beacon` protocol.
func (h *beaconHandler) RunPeer(peer *beacon.Peer, hand beacon.Handler) error {
	return (*handler)(h).runBeaconPeer(peer, hand)
}

// PeerInfo retrieves all known `beacon` information about a peer.
func (h *beaconHandler) PeerInfo(id enode.ID) interface{} {
	if p := h.peers.beacon(id.String()); p != nil {
		return p.info()
	}
	return nil
}

// Handle is a callback to be invoked when a data packet is received from
// the remote peer. Only packets not consumed by the protocol handler will
// be forwarded to the backend.
func (h *beaconHandler) Handle(peer *beacon.Peer, packet beacon.Packet) error {
	switch packet := packet.(type) {
	case *beacon.NewBlockHashesPacket:
		hashes, numbers := packet.Unpack()
		return h.handleBlockAnnounces(peer, hashes, numbers)

	case *beacon.NewBlockPacket:
		return h.handleBlockBroadcast(peer, packet)

	default:
		return fmt.Errorf("unexpected beacon packet type: %T", packet)
	}
}

// handleBlockAnnounces is invoked from a peer's message handler when it transmits a
// batch of block announcements for the local node to process.
func (h *beaconHandler) handleBlockAnnounces(peer *beacon.Peer, hashes []common.Hash, numbers []uint64) error {
	// Schedule all the unknown hashes for retrieval
	var (
		unknownHashes  = make([]common.Hash, 0, len(hashes))
		unknownNumbers = make([]uint64, 0, len(numbers))
	)
	for i := 0; i < len(hashes); i++ {
		if !h.chain.HasBlock(hashes[i], numbers[i]) {
			unknownHashes = append(unknownHashes, hashes[i])
			unknownNumbers = append(unknownNumbers, numbers[i])
		}
	}
	for i := 0; i < len(unknownHashes); i++ {
		h.blockFetcher.Notify(peer.ID(), unknownHashes[i], unknownNumbers[i], time.Now())
	}
	return nil
}

// handleBlockBroadcast is invoked from a peer's message handler when it transmits a
// block broadcast for the local node to process.
func (h *beaconHandler) handleBlockBroadcast(peer *beacon.Peer, packet *beacon.NewBlockPacket) error {
	block := packet.Block
	td := packet.TD

	// Schedule the block for import
	h.blockFetcher.Enqueue(peer.ID(), block)

	// Assuming the block is importable by the peer, but possibly not yet done so,
	// calculate the head hash and TD that the peer truly must have.
	var (
		trueHead = block.ParentHash()
		trueTD   = new(big.Int).Sub(td, block.Difficulty())
	)
	// Update the peer's total difficulty if better than the previous
	if _, td := peer.Head(); trueTD.Cmp(td) > 0 {
		peer.SetHead(trueHead, trueTD)
		h.chainSync.handlePeerEvent()
	}
	return nil
}
