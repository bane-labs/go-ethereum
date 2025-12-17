package beacon

import (
	"math/big"
	"math/rand/v2"
	"sync"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	// maxKnownBlocks is the maximum block hashes to keep in the known list
	// before starting to randomly evict them.
	maxKnownBlocks = 1024

	// maxQueuedBlocks is the maximum number of block propagations to queue up before
	// dropping broadcasts. There's not much point in queueing stale blocks, so a few
	// that might cover uncles should be enough.
	maxQueuedBlocks = 4

	// maxQueuedBlockAnns is the maximum number of block announcements to queue up before
	// dropping broadcasts. Similarly to block propagations, there's no point to queue
	// above some healthy uncle limit, so use that.
	maxQueuedBlockAnns = 4

	// maxKnownBlobs is the maximum block hashes to keep in the known list
	// before starting to randomly evict them.
	maxKnownBlobs = 512

	// blobBufferSize is the maximum number of batch blobs can be hold before sending
	blobBufferSize = 4
)

// Peer is a collection of relevant information we have about a `beacon` peer.
type Peer struct {
	id string // Unique ID for the peer, cached

	*p2p.Peer                   // The embedded P2P package peer
	rw        p2p.MsgReadWriter // Input/output streams for blob
	version   uint              // Protocol version negotiated

	head common.Hash // Latest advertised head block hash
	td   *big.Int    // Latest advertised head block total difficulty

	knownBlocks     *knownCache            // Set of block hashes known to be known by this peer
	queuedBlocks    chan *blockPropagation // Queue of blocks to broadcast to the peer
	queuedBlockAnns chan *types.Block      // Queue of blocks to announce to the peer

	knownBlobs        *knownCache         // Set of blob hashes known to be known by this peer
	blobBroadcast     chan core.BlobEvent // Channel used to queue blobs propagation requests
	blobRootBroadcast chan common.Hash    // Channel used to queue blobs block hash announcement requests

	reqDispatch chan *request  // Dispatch channel to send requests and track then until fulfillment
	reqCancel   chan *cancel   // Dispatch channel to cancel pending requests and untrack them
	resDispatch chan *response // Dispatch channel to fulfil pending requests and untrack them

	term chan struct{} // Termination channel to stop the broadcasters
	lock sync.RWMutex  // Mutex protecting the internal fields
}

// NewPeer create a wrapper for a network connection and negotiated protocol
// version.
func NewPeer(version uint, p *p2p.Peer, rw p2p.MsgReadWriter) *Peer {
	id := p.ID().String()
	peer := &Peer{
		id:                id,
		Peer:              p,
		rw:                rw,
		version:           version,
		knownBlocks:       newKnownCache(maxKnownBlocks),
		queuedBlocks:      make(chan *blockPropagation, maxQueuedBlocks),
		queuedBlockAnns:   make(chan *types.Block, maxQueuedBlockAnns),
		knownBlobs:        newKnownCache(maxKnownBlobs),
		blobBroadcast:     make(chan core.BlobEvent, blobBufferSize),
		blobRootBroadcast: make(chan common.Hash, blobBufferSize),
		reqDispatch:       make(chan *request),
		reqCancel:         make(chan *cancel),
		resDispatch:       make(chan *response),
		term:              make(chan struct{}),
	}
	go peer.broadcastBlocks()
	go peer.broadcastBlockBlob()
	go peer.dispatcher()

	return peer
}

// Close signals the broadcast goroutine to terminate. Only ever call this if
// you created the peer yourself via NewPeer. Otherwise let whoever created it
// clean it up!
func (p *Peer) Close() {
	close(p.term)
}

// ID retrieves the peer's unique identifier.
func (p *Peer) ID() string {
	return p.id
}

// Version retrieves the peer's negotiated `beacon` protocol version.
func (p *Peer) Version() uint {
	return p.version
}

// Head retrieves the current head hash and total difficulty of the peer.
func (p *Peer) Head() (hash common.Hash, td *big.Int) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	copy(hash[:], p.head[:])
	return hash, new(big.Int).Set(p.td)
}

// SetHead updates the head hash and total difficulty of the peer.
func (p *Peer) SetHead(hash common.Hash, td *big.Int) {
	p.lock.Lock()
	defer p.lock.Unlock()

	copy(p.head[:], hash[:])
	p.td.Set(td)
}

// KnownBlock returns whether peer is known to already have a block.
func (p *Peer) KnownBlock(hash common.Hash) bool {
	return p.knownBlocks.Contains(hash)
}

// markBlock marks a block as known for the peer, ensuring that the block will
// never be propagated to this particular peer.
func (p *Peer) markBlock(hash common.Hash) {
	// If we reached the memory allowance, drop a previously known block hash
	p.knownBlocks.Add(hash)
}

// SendNewBlockHashes announces the availability of a number of blocks through
// a hash notification.
func (p *Peer) SendNewBlockHashes(hashes []common.Hash, numbers []uint64) error {
	// Mark all the block hashes as known, but ensure we don't overflow our limits
	p.knownBlocks.Add(hashes...)

	request := make(NewBlockHashesPacket, len(hashes))
	for i := 0; i < len(hashes); i++ {
		request[i].Hash = hashes[i]
		request[i].Number = numbers[i]
	}
	return p2p.Send(p.rw, NewBlockHashesMsg, request)
}

// AsyncSendNewBlockHash queues the availability of a block for propagation to a
// remote peer. If the peer's broadcast queue is full, the event is silently
// dropped.
func (p *Peer) AsyncSendNewBlockHash(block *types.Block) {
	select {
	case p.queuedBlockAnns <- block:
		// Mark all the block hash as known, but ensure we don't overflow our limits
		p.knownBlocks.Add(block.Hash())
	default:
		p.Log().Debug("Dropping block announcement", "number", block.NumberU64(), "hash", block.Hash())
	}
}

// SendNewBlock propagates an entire block to a remote peer.
func (p *Peer) SendNewBlock(block *types.Block, td *big.Int) error {
	// Mark all the block hash as known, but ensure we don't overflow our limits
	p.knownBlocks.Add(block.Hash())
	return p2p.Send(p.rw, NewBlockMsg, &NewBlockPacket{
		Block: block,
		TD:    td,
	})
}

// AsyncSendNewBlock queues an entire block for propagation to a remote peer. If
// the peer's broadcast queue is full, the event is silently dropped.
func (p *Peer) AsyncSendNewBlock(block *types.Block, td *big.Int) {
	select {
	case p.queuedBlocks <- &blockPropagation{block: block, td: td}:
		// Mark all the block hash as known, but ensure we don't overflow our limits
		p.knownBlocks.Add(block.Hash())
	default:
		p.Log().Debug("Dropping block propagation", "number", block.NumberU64(), "hash", block.Hash())
	}
}

// ReplyBlobsRLP is the response to GetBlobs.
func (p *Peer) ReplyBlobsRLP(id uint64, blobs rlp.RawValue) error {
	return p2p.Send(p.rw, BlobsMsg, &BlobsRLPPacket{
		RequestId:        id,
		BlobsRLPResponse: blobs,
	})
}

// RequestBlobs sends GetBlobsMsg by request block hash.
func (p *Peer) RequestBlobs(hash common.Hash, sink chan *Response, ttl uint8) (*Request, error) {
	id := rand.Uint64()

	req := &Request{
		id:   id,
		sink: sink,
		code: GetBlobsMsg,
		want: BlobsMsg,
		data: &GetBlobsPacket{
			RequestId: id,
			BlockHash: hash,
			Ttl:       ttl,
		},
	}

	if err := p.dispatchRequest(req); err != nil {
		return nil, err
	}
	return req, nil
}

// KnownBlockBlobs returns whether peer is known to already have a block's blobs.
func (p *Peer) KnownBlockBlobs(blockHash common.Hash) bool {
	return p.knownBlobs.Contains(blockHash)
}

// markBlockBlobs marks blobs as known for the peer, ensuring that they
// will never be repropagated to this particular peer.
func (p *Peer) markBlockBlobs(blockHash common.Hash) {
	if !p.knownBlobs.Contains(blockHash) {
		// If we reached the memory allowance, drop a previously known block hash
		p.knownBlobs.Add(blockHash)
	}
}

// sendNewBlockBlobs propagates a block's blobs to the remote peer.
func (p *Peer) sendNewBlockBlobs(blockHash common.Hash, blobs types.BlobSidecars) error {
	// Mark all the blobs as known, but ensure we don't overflow our limits
	p.markBlockBlobs(blockHash)
	return p2p.Send(p.rw, NewBlobsMsg, &NewBlobsPacket{blockHash, blobs})
}

// AsyncSendNewBlockBlobs queues a batch of blob data for propagation to a remote peer. If
// the peer's broadcast queue is full, the event is silently dropped.
func (p *Peer) AsyncSendNewBlockBlobs(blockHash common.Hash, blobs types.BlobSidecars) {
	select {
	case p.blobBroadcast <- core.BlobEvent{BlockHash: blockHash, Sidecars: blobs}:
	case <-p.term:
		p.Log().Debug("Dropping blob propagation for closed peer", "block hash", blockHash)
	default:
		p.Log().Debug("Dropping blob propagation for abnormal peer", "block hash", blockHash)
	}
}

// sendNewBlobsRoot sends blobs block hash to the remote peer.
func (p *Peer) sendNewBlobsRoot(hash common.Hash) error {
	p.markBlockBlobs(hash)
	return p2p.Send(p.rw, NewBlobsRootMsg, &NewBlobsRootPacket{BlockHash: hash})
}

// AsyncSendNewBlobsRoot queues a blobs block hash announcement to a remote peer. If
// the peer's broadcast queue is full, the event is silently dropped.
func (p *Peer) AsyncSendNewBlobsRoot(hash common.Hash) {
	select {
	case p.blobRootBroadcast <- hash:
	case <-p.term:
		p.Log().Debug("Dropping blob block hash propagation for closed peer", "block hash", hash)
	default:
		p.Log().Debug("Dropping blob block hash propagation for abnormal peer", "block hash", hash)
	}
}

// knownCache is a cache for known hashes.
type knownCache struct {
	hashes mapset.Set[common.Hash]
	max    int
}

// newKnownCache creates a new knownCache with a max capacity.
func newKnownCache(max int) *knownCache {
	return &knownCache{
		max:    max,
		hashes: mapset.NewSet[common.Hash](),
	}
}

// Add adds a list of elements to the set.
func (k *knownCache) Add(hashes ...common.Hash) {
	for k.hashes.Cardinality() > max(0, k.max-len(hashes)) {
		k.hashes.Pop()
	}
	for _, hash := range hashes {
		k.hashes.Add(hash)
	}
}

// Contains returns whether the given item is in the set.
func (k *knownCache) Contains(hash common.Hash) bool {
	return k.hashes.Contains(hash)
}

// Cardinality returns the number of elements in the set.
func (k *knownCache) Cardinality() int {
	return k.hashes.Cardinality()
}
