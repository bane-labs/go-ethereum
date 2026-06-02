package beacon

import (
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie"
)

func handleNewBlockhashes(backend Backend, msg Decoder, peer *Peer) error {
	// A batch of new block announcements just arrived
	ann := new(NewBlockHashesPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	// Mark the hashes as present at the remote node
	for _, block := range *ann {
		peer.markBlock(block.Hash)
	}
	// Deliver them all to the backend for queuing
	return backend.Handle(peer, ann)
}

func handleNewBlock(backend Backend, msg Decoder, peer *Peer) error {
	// Retrieve and decode the propagated block
	ann := new(NewBlockPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	if hash := types.CalcUncleHash(ann.Block.Uncles()); hash != ann.Block.UncleHash() {
		log.Warn("Propagated block has invalid uncles", "have", hash, "exp", ann.Block.UncleHash())
		return nil // TODO(karalabe): return error eventually, but wait a few releases
	}
	if hash := types.DeriveSha(ann.Block.Transactions(), trie.NewStackTrie(nil)); hash != ann.Block.TxHash() {
		log.Warn("Propagated block has invalid body", "have", hash, "exp", ann.Block.TxHash())
		return nil // TODO(karalabe): return error eventually, but wait a few releases
	}
	ann.Block.ReceivedAt = msg.Time()
	ann.Block.ReceivedFrom = peer

	// Mark the peer as owning the block
	peer.markBlock(ann.Block.Hash())

	return backend.Handle(peer, ann)
}

func handleNewBlobsRoot(backend Backend, msg Decoder, peer *Peer) error {
	ann := new(NewBlobsRootPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}

	log.Debug("Receive NewBlobsRoot announcement", "from", peer.id, "blockHash", ann.BlockHash)

	peer.markBlockBlobs(ann.BlockHash)
	return backend.Handle(peer, ann)
}

func handleGetBlobs(backend Backend, msg Decoder, peer *Peer) error {
	req := new(GetBlobsPacket)
	if err := msg.Decode(req); err != nil {
		return fmt.Errorf("msg %v, decode err: %v", GetBlobsMsg, err)
	}

	log.Debug("Receive GetBlobs request", "from", peer.id, "req", req)

	if req.Ttl < 1 {
		log.Debug("GetBlobs request reached TTL limit", "from", peer.id, "req", req)
		return fmt.Errorf("invalid GetBlobs request, as the TTL limit has been reached, req block hash %s", req.BlockHash.Hex())
	}

	return backend.Handle(peer, req)
}

func handleBlobs(backend Backend, msg Decoder, peer *Peer) error {
	ann := new(BlobsPacket)
	switch peer.version {
	case BEACON1:
		ann1 := new(BlobsPacket1)
		if err := msg.Decode(ann1); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
		ann.RequestId = ann1.RequestId
		bss := make(types.BlobSidecars, 0, len(ann1.Sidecars))
		for _, sc := range ann1.Sidecars {
			bss = append(bss, types.NewBlobTxSidecar(types.BlobSidecarVersion0, sc.Blobs, sc.Commitments, sc.Proofs))
		}
		ann.Sidecars = bss
	case BEACON2:
		if err := msg.Decode(ann); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
	default:
		return fmt.Errorf("unknown beacon protocol version: %v", peer.version)
	}

	err := peer.dispatchResponse(&Response{
		id:   ann.RequestId,
		code: BlobsMsg,
		Res:  &ann.Sidecars,
	}, nil)
	log.Debug("Receive Blobs response", "from", peer.id, "requestId", ann.RequestId, "sidecars", len(ann.Sidecars), "err", err)
	return nil
}

func handleGetBatchBlobs(backend Backend, msg Decoder, peer *Peer) error {
	req := new(GetBatchBlobsPacket)
	if err := msg.Decode(req); err != nil {
		return fmt.Errorf("msg %v, decode err: %v", GetBatchBlobsMsg, err)
	}

	log.Debug("Receive GetBatchBlobs request", "from", peer.id, "req", req)

	return backend.Handle(peer, req)
}

func handleBatchBlobs(backend Backend, msg Decoder, peer *Peer) error {
	res := new(BatchBlobsPacket)
	switch peer.version {
	case BEACON1:
		res1 := new(BatchBlobsPacket1)
		if err := msg.Decode(res1); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
		bbr := make(BatchBlobsResponse, 0, len(res1.BatchBlobsResponse1))
		for _, scs1 := range res1.BatchBlobsResponse1 {
			scs := make(types.BlobSidecars, 0, len(scs1))
			for _, sc1 := range scs1 {
				scs = append(scs, types.NewBlobTxSidecar(types.BlobSidecarVersion0, sc1.Blobs, sc1.Commitments, sc1.Proofs))
			}
			bbr = append(bbr, scs)
		}
		res.RequestId = res1.RequestId
		res.BatchBlobsResponse = bbr
	case BEACON2:
		if err := msg.Decode(res); err != nil {
			return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
		}
	default:
		return fmt.Errorf("unknown beacon protocol version: %v", peer.version)
	}

	err := peer.dispatchResponse(&Response{
		id:   res.RequestId,
		code: BatchBlobsMsg,
		Res:  &res.BatchBlobsResponse,
	}, nil)
	log.Debug("Receive BatchBlobs response", "from", peer.id, "requestId", res.RequestId, "batchBlobs", len(res.BatchBlobsResponse), "err", err)
	return nil
}
