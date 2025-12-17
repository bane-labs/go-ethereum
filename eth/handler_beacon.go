package eth

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/beacon"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	// blobChanSize is the size of channel listening to NewBlobsEvent.
	blobChanSize = 1024
	// k is the number of peers to request blobs from when handling GetBlobs requests.
	k = 3
	// ttl is the time-to-live for blob requests.
	ttl = 3
	// Maximum allotted time to return an explicitly requested blob
	blobFetchTimeout = 500 * time.Millisecond
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

	case *beacon.NewBlobsPacket:
		return h.handleBlobsBroadcast(peer, packet)

	case *beacon.GetBlobsPacket:
		return h.handleGetBlobsPacket(peer, packet)

	case *beacon.NewBlobsRootPacket:
		return h.handleBlobsRootAnnounces(peer, packet)

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
	if h.beacon != nil {
		for i := 0; i < len(unknownHashes); i++ {
			h.beacon.NotifyBlockAnnon(peer.ID(), unknownHashes[i], unknownNumbers[i], time.Now())
		}
	}
	return nil
}

// handleBlockBroadcast is invoked from a peer's message handler when it transmits a
// block broadcast for the local node to process.
func (h *beaconHandler) handleBlockBroadcast(peer *beacon.Peer, packet *beacon.NewBlockPacket) error {
	block := packet.Block
	td := packet.TD

	// Schedule the block for import
	if h.beacon != nil {
		h.beacon.EnqueueBlock(peer.ID(), block)
	}

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

func (h *beaconHandler) handleBlobsBroadcast(_ *beacon.Peer, packet *beacon.NewBlobsPacket) error {
	// save blobs to local store
	return h.fs.InsertBlobs(packet.BlockHash, packet.Sidecars)
}

func (h *beaconHandler) handleGetBlobsPacket(peer *beacon.Peer, packet *beacon.GetBlobsPacket) error {
	if packet.Ttl > ttl {
		log.Debug("GetBlobs request with invalid TTL", "from", peer.ID(), "req", packet)
		// TODO: Suspicious peer. Should we drop it?
		return fmt.Errorf("invalid TTL %d for block hash %s", packet.Ttl, packet.BlockHash.Hex())
	}
	// check if the block has blob txs
	block := h.chain.GetBlockByHash(packet.BlockHash)
	if block == nil {
		log.Debug("GetBlobs request for unknown block", "from", peer.ID(), "req", packet)
		return fmt.Errorf("unknown block hash %s", packet.BlockHash.Hex())
	}
	if !block.HasBlobTxs() {
		log.Debug("GetBlobs request for block without blobs", "from", peer.ID(), "req", packet)
		return fmt.Errorf("block %s has no blobs", packet.BlockHash.Hex())
	}

	sidecars := h.fs.GetSidecarsByRoot(packet.BlockHash)
	if len(sidecars) > 0 {
		log.Debug("reply GetBlobs msg", "from", peer.ID(), "req block hash", packet.BlockHash, "sidecars", sidecars.Len())
		encoded, err := rlp.EncodeToBytes(sidecars)
		if err != nil {
			log.Error("Failed to encode blobs", "err", err)
			return err
		}
		return peer.ReplyBlobsRLP(packet.RequestId, encoded)
	}
	if packet.Ttl <= 1 {
		log.Debug("GetBlobs request reached TTL limit", "from", peer.ID(), "req", packet)
		return fmt.Errorf("blobs not found for block hash %s", packet.BlockHash.Hex())
	}

	blobData, err := h.retrieveSidecars(packet.BlockHash, packet.Ttl-1)
	if err != nil {
		log.Debug("failed to retrieve blobs for GetBlobs request", "from", peer.ID(), "req", packet, "err", err)
		return err
	}
	encoded, err := rlp.EncodeToBytes(blobData)
	if err != nil {
		log.Error("Failed to encode blobs", "err", err)
		return err
	}
	return peer.ReplyBlobsRLP(packet.RequestId, encoded)
}

func (h *beaconHandler) handleBlobsRootAnnounces(peer *beacon.Peer, packet *beacon.NewBlobsRootPacket) error {
	if !h.noPruning {
		// In pruning mode, we no need to fetch blobs
		return nil
	}

	// check if the block has blob txs
	block := h.chain.GetBlockByHash(packet.BlockHash)
	if block == nil {
		log.Debug("NewBlobsRootMsg request for unknown block", "from", peer.ID(), "req", packet)
		return fmt.Errorf("unknown block hash %s", packet.BlockHash.Hex())
	}
	if !block.HasBlobTxs() {
		log.Debug("NewBlobsRootMsg request for block without blobs", "from", peer.ID(), "req", packet)
		// TODO: Suspicious peer. Should we drop it?
		return fmt.Errorf("block %s has no blobs", packet.BlockHash.Hex())
	}

	// Check if it exists locally
	sidecars := h.fs.GetSidecarsByRoot(packet.BlockHash)
	if sidecars.Len() > 0 {
		return nil
	}

	// request blobs from peer
	resCh := make(chan *beacon.Response)
	defer close(resCh)
	ttl := uint8(1)
	req, err := peer.RequestBlobs(packet.BlockHash, resCh, ttl)
	if err != nil {
		log.Debug("failed to get blob data from peer", "block hash", packet.BlockHash, "peer", peer.ID(), "err", err)
		return err
	}
	defer req.Close()
	timeout := time.NewTimer(blobFetchTimeout * time.Duration(ttl))
	defer timeout.Stop()
	select {
	case <-timeout.C:
		return fmt.Errorf("not found blob data for block hash %s", packet.BlockHash.Hex())
	case res := <-resCh:
		res.Done <- nil
		blobs := res.Res.(*beacon.BlobsPacket).Sidecars
		log.Debug("requested blob data from peer", "block hash", packet.BlockHash, "peer", res.Req.Peer, "sidecars", len(blobs))

		if err = h.fs.InsertBlobs(packet.BlockHash, blobs); err != nil {
			return err
		}
	}

	return nil
}

func (h *beaconHandler) retrieveSidecars(blockHash common.Hash, ttl uint8) (types.BlobSidecars, error) {
	peers := h.peers.allBeacons()
	transfer := peers[:min(k, len(peers))]
	finishedCh := make(chan struct{})
	retrievedCh := make(chan struct{})
	var wg sync.WaitGroup
	resCh := make(chan *beacon.Response)
	defer close(resCh)
	for _, p := range transfer {
		wg.Add(1)
		go func(p *beacon.Peer) {
			defer wg.Done()
			req, err := p.RequestBlobs(blockHash, resCh, ttl)
			if err != nil {
				log.Debug("failed to get blob data from peer", "block hash", blockHash, "peer", p.ID(), "err", err)
				return
			}
			defer req.Close()

			timeout := time.NewTimer(blobFetchTimeout * time.Duration(ttl))
			defer timeout.Stop()
			select {
			case <-timeout.C:
				log.Debug("blob fetch timeout", "block hash", blockHash, "peer", p.ID())
				return
			case <-retrievedCh:
				return
			}
		}(p.Peer)
	}

	go func() {
		wg.Wait()
		close(finishedCh)
	}()

	select {
	case <-finishedCh:
		log.Debug("not found blob data from other peers", "block hash", blockHash)
		return nil, fmt.Errorf("not found blob data for block hash %s", blockHash.Hex())
	case res := <-resCh:
		res.Done <- nil
		blobs := res.Res.(*beacon.BlobsPacket).Sidecars
		// notify other goroutines to stop
		close(retrievedCh)
		log.Debug("requested blob data from peer", "block hash", blockHash, "peer", res.Req.Peer, "sidecars", len(blobs))

		// save blobs to local store
		if err := h.fs.InsertBlobs(blockHash, blobs); err != nil {
			log.Warn("failed to write blob sidecars", "block hash", blockHash, "err", err)
		}
		return blobs, nil
	}
}

// RetrieveSidecarsByRoot retrieves blob sidecars by block hash.
func (h *beaconHandler) RetrieveSidecarsByRoot(blockHash common.Hash) (types.BlobSidecars, error) {
	blobData, err := h.retrieveSidecars(blockHash, ttl)
	if err != nil {
		log.Debug("failed to retrieve blobs for GetBlobs request", "blockHash", blockHash, "err", err)
		return nil, err
	}
	return blobData, nil
}
