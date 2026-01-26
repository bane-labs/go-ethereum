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

func handleNewBlobs(backend Backend, msg Decoder, peer *Peer) error {
	ann := new(NewBlobsPacket)
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	if err := ann.sanityCheck(); err != nil {
		return err
	}

	// Schedule all the unknown hashes for retrieval
	peer.markBlockBlobs(ann.BlockHash)
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
	if err := msg.Decode(ann); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
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
	if err := msg.Decode(res); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}

	err := peer.dispatchResponse(&Response{
		id:   res.RequestId,
		code: BatchBlobsMsg,
		Res:  &res.BatchBlobsResponse,
	}, nil)
	log.Debug("Receive BatchBlobs response", "from", peer.id, "requestId", res.RequestId, "batchBlobs", len(res.BatchBlobsResponse), "err", err)
	return nil
}
