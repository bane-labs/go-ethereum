package downloader

import (
	"errors"
	"math/rand"
	"sort"
	"time"

	beaconSync "github.com/ethereum/go-ethereum/beacon/impl/synchronizer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft/light"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/log"
)

const rollBackBlocks = 2 // The number of blocks to roll back when requesting headers, and the extra 1 more block is for safe reorg

type beaconLightRequest struct {
	peer string // Peer to which this request is assigned
	id   uint64 // Request ID of this request

	deliver chan *beaconLightResponse // Channel to deliver successful response on
	revert  chan *beaconLightRequest  // Channel to deliver request failure on
	cancel  chan struct{}             // Channel to track sync cancellation
	stale   chan struct{}             // Channel to signal the request was dropped

	head  uint64 // Head number of the requested batch of headers
	count int    // Number of headers requested
}

// headerResponse is an already verified remote response to a header request.
type beaconLightResponse struct {
	peer           *peerConnection // Peer from which this response originates
	reqid          uint64          // Request ID that this response fulfils
	headers        []*types.Header // Chain of headers
	metas          []common.Hash   // Metadata of the header chain
	finalizedBlock *types.Block    // Finalized block corresponding to the header batch
	latestBlock    *types.Block    // Latest block corresponding to the header batch
}

type beaconLightSyncer struct {
	trustedHeader *types.Header                  // The header that has been extended to
	startHead     uint64                         // The head number of the first header batch to request
	requests      map[uint64]*beaconLightRequest // Map of pending header requests, keyed by request ID for easy lookup on response
	requestFails  chan *beaconLightRequest       // Channel to receive failed requests for rescheduling
	responses     chan *beaconLightResponse      // Channel to receive successful responses for processing
	idles         map[string]*peerConnection     // Map of idle peers available for assignment, keyed by peer ID for easy lookup

	scratchSpace  []*beaconLightResponse // Scratch space to accumulate response in (first = recent)
	scratchOwners []string               // Peer IDs owning chunks of the scratch space (pend or delivered)
	scratchHead   uint64                 // Block number of the first item in the scratch space

	downloader      *Downloader
	extend          beaconSync.BeaconExtendFn
	completeSyncing func()
}

func newBeaconLightSyncer(completeSyncing func(), downloader *Downloader, extend beaconSync.BeaconExtendFn) *beaconLightSyncer {
	return &beaconLightSyncer{
		requests:        make(map[uint64]*beaconLightRequest),
		requestFails:    make(chan *beaconLightRequest),
		responses:       make(chan *beaconLightResponse),
		idles:           make(map[string]*peerConnection),
		downloader:      downloader,
		extend:          extend,
		completeSyncing: completeSyncing,
	}
}

func (b *beaconLightSyncer) loop(start chan *types.Header) error {
	// Start tracking idle peers for task assignments
	peering := make(chan *peeringEvent, 64) // arbitrary buffer, just some burst protection

	peeringSub := b.downloader.peers.SubscribeEvents(peering)
	defer peeringSub.Unsubscribe()

	for _, peer := range b.downloader.peers.AllPeers() {
		b.idles[peer.id] = peer
	}

	completed := true
	var cancel chan struct{}
	for {
		if !completed {
			b.assignTasks(b.responses, b.requestFails, cancel)
		}
		// If the node is going down, unblock
		select {
		case event := <-peering:
			// A peer joined or left, the tasks queue and allocations need to be
			// checked for potential assignment or reassignment
			peerid := event.peer.id
			if event.join {
				log.Debug("Joining beacon light sync peer", "id", peerid)
				b.idles[peerid] = event.peer
			} else {
				log.Debug("Leaving beacon light sync peer", "id", peerid)
				b.revertRequests(peerid)
				delete(b.idles, peerid)
			}
		case header, ok := <-start:
			if !ok {
				return nil
			}
			if b.trustedHeader == nil || b.trustedHeader.Number.Uint64() < header.Number.Uint64() {
				log.Info("Starting beacon light sync", "head", header.Number.Uint64())
				// If the header is newer than the current trusted header, update the trusted header and start syncing
				b.trustedHeader = header
				b.startHead = header.Number.Uint64()
				completed = false
				cancel = make(chan struct{})

				b.scratchSpace = make([]*beaconLightResponse, beaconSync.ScratchSpaceLen)
				b.scratchOwners = make([]string, beaconSync.ScratchSpaceLen)
				b.scratchHead = header.Number.Uint64()
			}
		case req := <-b.requestFails:
			b.revertRequest(req)

		case res := <-b.responses:
			if syncCompleted, err := b.processResponse(res); err != nil {
				// If the response is invalid, ignore it
				log.Debug("Invalid beacon light response received", "err", err)
			} else if syncCompleted {
				// If the light sync process is complete, reset the state and wait for a new start signal to recover
				log.Info("Beacon light sync complete", "head", b.trustedHeader.Number.Uint64())
				b.completeSyncing()
				completed = syncCompleted
				close(cancel)
				b.requests = make(map[uint64]*beaconLightRequest)
				b.scratchSpace = nil
				b.scratchHead = 0
				b.scratchOwners = nil
				b.startHead = 0
			}
			// We still have work to do, loop and repeat

		case <-b.downloader.quitCh:
			return nil
		}
	}
}

// assignTasks attempts to match idle peers to pending header retrievals.
func (b *beaconLightSyncer) assignTasks(success chan *beaconLightResponse, fail chan *beaconLightRequest, cancel chan struct{}) {
	// Sort the peers by download capacity to use faster ones if many available
	idlers := &peerCapacitySort{
		peers: make([]*peerConnection, 0, len(b.idles)),
		caps:  make([]int, 0, len(b.idles)),
	}
	targetTTL := b.downloader.peers.rates.TargetTimeout()
	for _, peer := range b.idles {
		idlers.peers = append(idlers.peers, peer)
		idlers.caps = append(idlers.caps, b.downloader.peers.rates.Capacity(peer.id, eth.BlockHeadersMsg, targetTTL))
	}
	if len(idlers.peers) == 0 {
		return
	}
	sort.Sort(idlers)

	// Find header regions not yet downloading and fill them
	for task, owner := range b.scratchOwners {
		// If we're out of idle peers, stop assigning tasks
		if len(idlers.peers) == 0 {
			return
		}
		// Skip any tasks already filling
		if owner != "" {
			continue
		}
		// Found a task and have peers available, assign it
		idle := idlers.peers[0]

		idlers.peers = idlers.peers[1:]
		idlers.caps = idlers.caps[1:]

		// Matched a pending task to an idle peer, allocate a unique request id
		var reqid uint64
		for {
			reqid = uint64(rand.Int63())
			if reqid == 0 {
				continue
			}
			if _, ok := b.requests[reqid]; ok {
				continue
			}
			break
		}
		// Generate the network query and send it to the peer
		req := &beaconLightRequest{
			peer:    idle.id,
			id:      reqid,
			deliver: success,
			revert:  fail,
			cancel:  cancel,
			stale:   make(chan struct{}),

			head:  b.scratchHead + uint64(task*beaconSync.ExpectedHeadersNum),
			count: beaconSync.ExpectedHeadersNum,
		}
		// If it's not the first task, roll back `rollBackBlocks` blocks to ensure a better match.
		if b.scratchHead != b.startHead {
			req.head -= rollBackBlocks
			req.count += rollBackBlocks
		}
		b.requests[reqid] = req
		delete(b.idles, idle.id)

		// Generate the network query and send it to the peer
		go b.executeTask(idle, req)

		// Inject the request into the task to block further assignments
		b.scratchOwners[task] = idle.id
	}
}

// executeTask executes a single fetch request, blocking until either a result
// arrives or a timeouts / cancellation is triggered. The method should be run
// on its own goroutine and will deliver on the requested channels.
func (b *beaconLightSyncer) executeTask(peer *peerConnection, req *beaconLightRequest) {
	start := time.Now()
	resCh := make(chan *eth.Response)

	// If it is not the first task, request an additional `rollBackBlocks` blocks to make up
	// for the missing number and facilitate the calculation of the task position index.
	peer.log.Debug("Attempting to retrieve new headers", "peer", peer.id, "head", req.head, "count", req.count)
	netreq, err := peer.peer.RequestHeadersByNumber(req.head, req.count, 0, false, resCh)
	if err != nil {
		peer.log.Trace("Failed to request headers", "err", err)
		b.scheduleRevertRequest(req)
		return
	}
	defer netreq.Close()

	// Wait until the response arrives, the request is cancelled or times out
	ttl := b.downloader.peers.rates.TargetTimeout()

	timeoutTimer := time.NewTimer(ttl)
	defer timeoutTimer.Stop()

	select {
	case <-req.cancel:
		peer.log.Debug("Header request cancelled")
		b.scheduleRevertRequest(req)

	case <-timeoutTimer.C:
		// Header retrieval timed out, update the metrics
		peer.log.Warn("Header request timed out, dropping peer", "elapsed", ttl)
		headerTimeoutMeter.Mark(1)
		b.downloader.peers.rates.Update(peer.id, eth.BlockHeadersMsg, 0, 0)
		b.scheduleRevertRequest(req)

		b.downloader.dropPeer(peer.id)

	case res := <-resCh:
		// Headers successfully retrieved, update the metrics
		headers := *res.Res.(*eth.BlockHeadersRequest)
		metas := res.Meta.([]common.Hash)

		headerReqTimer.Update(time.Since(start))
		b.downloader.peers.rates.Update(peer.id, eth.BlockHeadersMsg, res.Time, len(headers))

		switch {
		case len(headers) == 0:
			// Since we don't know the latest block height, the current termination
			// method is rather rudimentary. It uses the returned number to determine
			// whether to end or not. So this might be normal. Let's have it retry.
			res.Done <- nil
			b.scheduleRevertRequest(req)
		case headers[0].Number.Uint64() != req.head:
			// Header batch anchored at non-requested number
			peer.log.Debug("Invalid header response head", "have", headers[0].Number, "want", req.head)
			res.Done <- errors.New("invalid header batch anchor")
			b.scheduleRevertRequest(req)
		default:
			res.Done <- nil

			// If there's no new headers to sync
			if len(metas) < 2 {
				select {
				case req.deliver <- &beaconLightResponse{
					peer:    peer,
					reqid:   req.id,
					headers: headers,
					metas:   metas,
				}:
				case <-req.cancel:
				}
				return
			}
			// Verifiy based on our light client rules, if it fails, retry
			valid := light.VerifyHeaders(headers)
			if !valid {
				log.Debug("Received invalid new headers", "start", headers[0].Hash(), "end", headers[len(headers)-1].Hash())
				b.scheduleRevertRequest(req)
				b.downloader.dropPeer(peer.id)
				return
			}
			// If the verification is successful, update the trusted hash and repeat until
			// the light chain is extended. Here we take n-1, since n may get reorg
			trusted := headers[len(headers)-2]
			latest := headers[len(headers)-1]
			bodies, _, err := b.downloader.fetchBodiesByHash(peer, []common.Hash{trusted.Hash(), latest.Hash()})
			if err != nil {
				// If downloader is canceled, then abort and wait for a new start
				if err == errCanceled {
					b.scheduleRevertRequest(req)
					return
				}
				log.Debug("Failed to fetch new bodies", "err", err)
				b.scheduleRevertRequest(req)
				b.downloader.dropPeer(peer.id)
				return
			}
			if len(bodies) != 2 {
				log.Debug("Received invalid number of new bodies", "len", len(bodies))
				b.scheduleRevertRequest(req)
				b.downloader.dropPeer(peer.id)
				return
			}
			// Verify the bodies match the headers, if not, retry
			// Rebuild the block trusted to be finalized
			body := types.Body{
				Transactions: bodies[0].Transactions,
				Uncles:       bodies[0].Uncles,
				Withdrawals:  bodies[0].Withdrawals,
			}
			finalizedBlock := types.NewBlockWithHeader(trusted).WithBody(body)
			// Rebuild the block temporarily latest
			body = types.Body{
				Transactions: bodies[1].Transactions,
				Uncles:       bodies[1].Uncles,
				Withdrawals:  bodies[1].Withdrawals,
			}
			latestBlock := types.NewBlockWithHeader(latest).WithBody(body)
			if finalizedBlock.Hash() != trusted.Hash() || latestBlock.Hash() != latest.Hash() {
				log.Debug("Received invalid new bodies", "trusted", trusted.Hash(), "latest", latest.Hash())
				b.scheduleRevertRequest(req)
				b.downloader.dropPeer(peer.id)
				return
			}
			select {
			case req.deliver <- &beaconLightResponse{
				peer:           peer,
				reqid:          req.id,
				headers:        headers,
				metas:          metas,
				finalizedBlock: finalizedBlock,
				latestBlock:    latestBlock,
			}:
			case <-req.cancel:
			}
		}
	}
}

// revertRequests locates all the currently pending requests from a particular
// peer and reverts them, rescheduling for others to fulfill.
func (b *beaconLightSyncer) revertRequests(peer string) {
	// Gather the requests first, revertals need the lock too
	var requests []*beaconLightRequest
	for _, req := range b.requests {
		if req.peer == peer {
			requests = append(requests, req)
		}
	}
	// Revert all the requests matching the peer
	for _, req := range requests {
		b.revertRequest(req)
	}
}

// scheduleRevertRequest asks the event loop to clean up a request and return
// all failed retrieval tasks to the scheduler for reassignment.
func (b *beaconLightSyncer) scheduleRevertRequest(req *beaconLightRequest) {
	select {
	case req.revert <- req:
		// Sync event loop notified
	case <-req.cancel:
		// Sync cycle got cancelled
	case <-req.stale:
		// Request already reverted
	}
}

// revertRequest cleans up a request and returns all failed retrieval tasks to
// the scheduler for reassignment.
//
// Note, this needs to run on the event runloop thread to reschedule to idle peers.
// On peer threads, use scheduleRevertRequest.
func (b *beaconLightSyncer) revertRequest(req *beaconLightRequest) {
	log.Trace("Reverting header request", "peer", req.peer, "reqid", req.id)
	select {
	case <-req.stale:
		log.Trace("Header request already reverted", "peer", req.peer, "reqid", req.id)
		return
	default:
	}
	close(req.stale)

	// Remove the request from the tracked set
	delete(b.requests, req.id)

	// Remove the request from the tracked set and mark the task as not-pending,
	// ready for rescheduling
	head := req.head
	if b.scratchHead != b.startHead {
		head += rollBackBlocks
	}
	b.scratchOwners[(head-b.scratchHead)/beaconSync.ExpectedHeadersNum] = ""
}

func (b *beaconLightSyncer) processResponse(res *beaconLightResponse) (completed bool, err error) {
	res.peer.log.Trace("Processing header response", "head", res.headers[0].Number, "hash", res.headers[0].Hash(), "count", len(res.headers))

	// Whether the response is valid, we can mark the peer as idle and notify
	// the scheduler to assign a new task. If the response is invalid, we'll
	// drop the peer in a bit.
	b.idles[res.peer.id] = res.peer

	// Ensure the response is for a valid request
	if _, ok := b.requests[res.reqid]; !ok {
		// Some internal accounting is broken. A request either times out or it
		// gets fulfilled successfully. It should not be possible to deliver a
		// response to a non-existing request.
		res.peer.log.Error("Unexpected header packet")
		return false, errors.New("Unexpected header packet")
	}
	delete(b.requests, res.reqid)

	head := res.headers[0].Number.Uint64()
	if b.scratchHead != b.startHead {
		head += rollBackBlocks
	}
	b.scratchSpace[(head-b.scratchHead)/beaconSync.ExpectedHeadersNum] = res

	// If there's still a gap in the head of the scratch space, abort
	if b.scratchSpace[0] == nil {
		return false, nil
	}
	// Try to consume any head headers, validating the boundary conditions
	for b.scratchSpace[0] != nil {
		nextResp := b.scratchSpace[0]
		if len(nextResp.metas) < 2 {
			// Nothing to extend, and wait for a new start signal to recover
			return true, nil
		}

		if b.trustedHeader.Hash() != nextResp.metas[0] {
			log.Debug("Received invalid new headers start", "want", b.trustedHeader.Hash(), "have", nextResp.metas[0])

			b.scratchSpace[0] = nil
			b.downloader.dropPeer(b.scratchOwners[0])
			b.scratchOwners[0] = ""
			break
		}

		log.Debug("Successfully fetch light headers", "start", nextResp.headers[0].Hash(), "blocks", len(nextResp.headers))

		requestCount := beaconSync.ExpectedHeadersNum
		if b.scratchHead != b.startHead {
			requestCount += rollBackBlocks
		}
		if len(nextResp.metas) < requestCount {
			completed = true
		}
		// Try to extend the chain trusted to pending headers from the network
		var linked bool
		var err error
		if linked, err = b.extend(nextResp.metas, nextResp.finalizedBlock, nextResp.latestBlock); err != nil {
			// Wait for a new start signal to recover
			return true, nil
		}
		b.trustedHeader = nextResp.headers[len(nextResp.headers)-2]
		// If the chain is not extended and we have more headers, keep consuming, otherwise wait for the next batch to extend
		if completed || linked {
			return true, nil
		}

		// Response consumed, shift the download window forward
		copy(b.scratchSpace, b.scratchSpace[1:])
		b.scratchSpace[len(b.scratchSpace)-1] = nil
		copy(b.scratchOwners, b.scratchOwners[1:])
		b.scratchOwners[len(b.scratchSpace)-1] = ""

		b.scratchHead += beaconSync.ExpectedHeadersNum
	}
	return false, nil
}

// BeaconLightSync is a light client protocol for only dBFT to verify the beacon
// sync target header by hash. This grants the consensus layer the ability to
// verify the beacon sync target header without executing the chain history, but
// only checking the signatures against the NextConsensus specification.
func (d *Downloader) BeaconLightSync(completeSyncing func(), extend beaconSync.BeaconExtendFn, start chan *types.Header) error {
	lightSync := newBeaconLightSyncer(completeSyncing, d, extend)
	return lightSync.loop(start)
}
