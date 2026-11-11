package downloader

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	beaconSync "github.com/ethereum/go-ethereum/beacon/impl/synchronizer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft/light"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/bal"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
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

	peers *peerSet                   // Set of peers we can sync from
	idles map[string]*peerConnection // Map of idle peers available for assignment, keyed by peer ID for easy lookup
	drop  peerDropFn                 // Drops a peer for misbehaving

	scratchSpace  []*beaconLightResponse // Scratch space to accumulate response in (first = recent)
	scratchOwners []string               // Peer IDs owning chunks of the scratch space (pend or delivered)
	scratchHead   uint64                 // Block number of the first item in the scratch space

	terminate chan struct{} // Termination channel to abort sync

	extend   beaconSync.BeaconExtendFn // Callback to extend the beacon trust
	complete func()                    // Callback to signal sync completion
}

func newBeaconLightSyncer(peers *peerSet, drop peerDropFn, terminate chan struct{}, extend beaconSync.BeaconExtendFn, complete func()) *beaconLightSyncer {
	return &beaconLightSyncer{
		requests:     make(map[uint64]*beaconLightRequest),
		requestFails: make(chan *beaconLightRequest),
		responses:    make(chan *beaconLightResponse),
		idles:        make(map[string]*peerConnection),
		peers:        peers,
		drop:         drop,
		terminate:    terminate,
		extend:       extend,
		complete:     complete,
	}
}

func (b *beaconLightSyncer) loop(start chan *types.Header) error {
	// Start tracking idle peers for task assignments
	peering := make(chan *peeringEvent, 64) // arbitrary buffer, just some burst protection

	peeringSub := b.peers.SubscribeEvents(peering)
	defer peeringSub.Unsubscribe()

	for _, peer := range b.peers.AllPeers() {
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
			log.Info("Starting beacon light sync", "head", header.Number.Uint64())
			// If the header is newer than the current trusted header, update the trusted header and start syncing
			b.trustedHeader = header
			b.startHead = header.Number.Uint64()
			completed = false
			cancel = make(chan struct{})

			b.scratchSpace = make([]*beaconLightResponse, beaconSync.ScratchSpaceLen)
			b.scratchOwners = make([]string, beaconSync.ScratchSpaceLen)
			b.scratchHead = header.Number.Uint64()
		case req := <-b.requestFails:
			b.revertRequest(req)

		case res := <-b.responses:
			if shouldComplete, err := b.processResponse(res); err != nil {
				// If the response is invalid, ignore it
				log.Debug("Invalid beacon light response received", "err", err)
			} else if shouldComplete {
				// Call the complete callback and close the cancel channel to stop the sync
				b.complete()
				completed = true
				close(cancel)
				// Reset the state and wait for a new start signal to recover
				b.requests = make(map[uint64]*beaconLightRequest)
				b.scratchSpace = nil
				b.scratchHead = 0
				b.scratchOwners = nil
				b.startHead = 0
				log.Info("Beacon light sync complete", "head", b.trustedHeader.Number.Uint64())
			}
			// Otherwise there's still work to do, loop and repeat

		case <-b.terminate:
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
	targetTTL := b.peers.rates.TargetTimeout()
	for _, peer := range b.idles {
		idlers.peers = append(idlers.peers, peer)
		idlers.caps = append(idlers.caps, b.peers.rates.Capacity(peer.id, eth.BlockHeadersMsg, targetTTL))
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
		if req.head != b.startHead {
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
	ttl := b.peers.rates.TargetTimeout()

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
		b.peers.rates.Update(peer.id, eth.BlockHeadersMsg, 0, 0)
		b.scheduleRevertRequest(req)

		b.drop(peer.id)

	case res := <-resCh:
		// Headers successfully retrieved, update the metrics
		headers := *res.Res.(*eth.BlockHeadersRequest)
		metas := res.Meta.([]common.Hash)

		headerReqTimer.Update(time.Since(start))
		b.peers.rates.Update(peer.id, eth.BlockHeadersMsg, res.Time, len(headers))

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
				b.drop(peer.id)
				return
			}

			// If the verification is successful, update the trusted hash and repeat until
			// the light chain is extended. Here we take n-1, since n may get reorg
			b.rebuildBlocks(peer, req, headers, metas)
		}
	}
}

func (b *beaconLightSyncer) rebuildBlocks(peer *peerConnection, req *beaconLightRequest, headers []*types.Header, metas []common.Hash) {
	const expectedResponseNum = 2 // We expect to receive 2 blocks, one for the trusted block and one for the latest block
	trusted := headers[len(headers)-2]
	latest := headers[len(headers)-1]
	requestHeaders := []*types.Header{trusted, latest}
	reqHashes := []common.Hash{trusted.Hash(), latest.Hash()}
	balsCh := make(chan []*bal.BlockAccessList)

	// toFetchBals is set if any of the blocks have a non-empty access list hash
	toFetchBals := false
	for _, header := range requestHeaders {
		if header.BlockAccessListHash != nil && !header.EmptyBlockAccessListHash() {
			toFetchBals = true
		}
	}
	// Fetch the access lists in parallel with the bodies, since they are independent and can be retrieved concurrently
	go func() {
		defer close(balsCh)

		if !toFetchBals {
			select {
			case <-req.cancel:
			case balsCh <- []*bal.BlockAccessList{{}, {}}:
			}
			return
		}
		hashes, bals, err := b.fetchBALsByHash(peer, req.cancel, reqHashes)
		if err != nil {
			// If downloader is canceled, then abort and wait for a new start
			if err == errCanceled {
				b.scheduleRevertRequest(req)
				return
			}
			log.Debug("Failed to fetch new BALs", "err", err)
			b.scheduleRevertRequest(req)
			b.drop(peer.id)
			return
		}
		if len(bals) != expectedResponseNum {
			log.Debug("Received invalid number of new BALs", "len", len(bals))
			b.scheduleRevertRequest(req)
			b.drop(peer.id)
			return
		}
		lists := make([]*bal.BlockAccessList, len(requestHeaders))
		for i, header := range requestHeaders {
			list, err := decodeBlockAccessList(header, hashes[i], bals[i])
			if err != nil {
				log.Debug("Received invalid BAL", "number", header.Number, "hash", header.Hash(), "err", err)
				b.scheduleRevertRequest(req)
				b.drop(peer.id)
				return
			}
			lists[i] = list
		}
		select {
		case <-req.cancel:
		case balsCh <- lists:
		}
	}()

	hashes, bodies, err := b.fetchBodiesByHash(peer, req.cancel, reqHashes)
	if err != nil {
		// If downloader is canceled, then abort and wait for a new start
		if err == errCanceled {
			b.scheduleRevertRequest(req)
			return
		}
		log.Debug("Failed to fetch new bodies", "err", err)
		b.scheduleRevertRequest(req)
		b.drop(peer.id)
		return
	}
	if len(bodies) != expectedResponseNum {
		log.Debug("Received invalid number of new bodies", "len", len(bodies))
		b.scheduleRevertRequest(req)
		b.drop(peer.id)
		return
	}

	// Decode
	decodeBody := func(header *types.Header, index int) (*types.Body, error) {
		if hashes.TransactionRoots[index] != header.TxHash {
			return nil, errInvalidBody
		}
		if hashes.UncleHashes[index] != header.UncleHash {
			return nil, errInvalidBody
		}
		if header.WithdrawalsHash == nil {
			// Nil hash means that withdrawals should not be present in body
			if bodies[index].Withdrawals != nil {
				return nil, errInvalidBody
			}
		} else { // Non-nil hash: body must have withdrawals
			if bodies[index].Withdrawals == nil {
				return nil, errInvalidBody
			}
			if hashes.WithdrawalRoots[index] != *header.WithdrawalsHash {
				return nil, errInvalidBody
			}
		}

		var err error
		var withdrawals []*types.Withdrawal
		txs, err := bodies[index].Transactions.Items()
		if err != nil {
			log.Debug(fmt.Errorf("%w: bad transactions: %v", errInvalidBody, err).Error())
			return nil, err
		}
		uncles, err := bodies[index].Uncles.Items()
		if err != nil {
			log.Debug(fmt.Errorf("%w: bad uncles: %v", errInvalidBody, err).Error())
			return nil, err
		}
		if bodies[index].Withdrawals != nil {
			withdrawals, err = bodies[index].Withdrawals.Items()
			if err != nil {
				log.Debug(fmt.Errorf("%w: bad withdrawals: %v", errInvalidBody, err).Error())
				return nil, err
			}
		}
		return &types.Body{
			Transactions: txs,
			Uncles:       uncles,
			Withdrawals:  withdrawals,
		}, nil
	}

	var decodedBodies [expectedResponseNum]*types.Body
	for i, header := range requestHeaders {
		body, err := decodeBody(header, i)
		if err != nil {
			log.Debug("Failed to decode body", "err", err)
			return
		}
		decodedBodies[i] = body
	}

	var bals []*bal.BlockAccessList
	select {
	case <-req.cancel:
		return
	case bals = <-balsCh:
	}
	if len(bals) != expectedResponseNum {
		log.Error("Received invalid number of new BALs", "len", len(bals))
		return
	}

	// Verify the bodies match the headers, if not, retry
	// Rebuild the block trusted to be finalized
	finalizedBlock := types.NewBlockWithHeader(trusted).WithBody(*decodedBodies[0])
	if finalizedBlock.Header().BlockAccessListHash != nil {
		finalizedBlock = finalizedBlock.WithAccessListUnsafe(bals[0])
	}
	// Rebuild the block temporarily latest
	latestBlock := types.NewBlockWithHeader(latest).WithBody(*decodedBodies[1])
	if latestBlock.Header().BlockAccessListHash != nil {
		latestBlock = latestBlock.WithAccessListUnsafe(bals[1])
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

func decodeBlockAccessList(header *types.Header, hash common.Hash, data []byte) (*bal.BlockAccessList, error) {
	if bytes.Equal(data, rlp.EmptyString) {
		switch {
		case header.BlockAccessListHash == nil:
			return nil, nil
		case header.EmptyBlockAccessListHash():
			return &bal.BlockAccessList{}, nil
		default:
			return nil, errors.New("missing BAL for block with non-empty block access list hash")
		}
	}
	if header.BlockAccessListHash == nil || hash != *header.BlockAccessListHash {
		return nil, errors.New("block access list hash mismatch")
	}
	list := new(bal.BlockAccessList)
	if err := rlp.DecodeBytes(data, list); err != nil {
		return nil, fmt.Errorf("failed to decode BAL: %w", err)
	}
	return list, nil
}

func (b *beaconLightSyncer) fetchBodiesByHash(p *peerConnection, cancel chan struct{}, hashes []common.Hash) (eth.BlockBodyHashes, []eth.BlockBody, error) {
	// Create the response sink and send the network request
	start := time.Now()
	resCh := make(chan *eth.Response)

	req, err := p.peer.RequestBodies(hashes, resCh)
	if err != nil {
		return eth.BlockBodyHashes{}, nil, err
	}
	defer req.Close()

	// Wait until the response arrives, the request is cancelled or times out
	ttl := b.peers.rates.TargetTimeout()

	timeoutTimer := time.NewTimer(ttl)
	defer timeoutTimer.Stop()

	select {
	case <-cancel:
		return eth.BlockBodyHashes{}, nil, errCanceled

	case <-timeoutTimer.C:
		// Body retrieval timed out, update the metrics
		p.log.Debug("Body request timed out", "elapsed", ttl)
		bodyTimeoutMeter.Mark(1)

		return eth.BlockBodyHashes{}, nil, errTimeout

	case res := <-resCh:
		resp := res.Res.(*eth.BlockBodiesResponse)
		meta := res.Meta.(eth.BlockBodyHashes)
		// Bodies successfully retrieved, update the metrics
		bodyReqTimer.Update(time.Since(start))
		bodyInMeter.Mark(int64(len(*resp)))

		// Don't reject the packet even if it turns out to be bad, downloader will
		// disconnect the peer on its own terms. Simply delivery the bodies to
		// be processed by the caller
		res.Done <- nil

		return meta, *resp, nil
	}
}

func (b *beaconLightSyncer) fetchBALsByHash(p *peerConnection, cancel chan struct{}, hashes []common.Hash) ([]common.Hash, eth.BlockAccessListResponse, error) {
	// Create the response sink and send the network request
	start := time.Now()
	resCh := make(chan *eth.Response)

	req, err := p.peer.RequestBALs(hashes, resCh)
	if err != nil {
		return nil, nil, err
	}
	defer req.Close()

	// Wait until the response arrives, the request is cancelled or times out
	ttl := b.peers.rates.TargetTimeout()

	timeoutTimer := time.NewTimer(ttl)
	defer timeoutTimer.Stop()

	select {
	case <-cancel:
		return nil, nil, errCanceled

	case <-timeoutTimer.C:
		// BAL retrieval timed out, update the metrics
		p.log.Debug("BAL request timed out", "elapsed", ttl)
		balTimeoutMeter.Mark(1)

		return nil, nil, errTimeout

	case res := <-resCh:
		bals := *res.Res.(*eth.BlockAccessListResponse)
		hashes := res.Meta.([]common.Hash) // {keccak256 hash per entry, zero hash if unavailable}
		// BALs successfully retrieved, update the metrics
		balReqTimer.Update(time.Since(start))
		balInMeter.Mark(int64(len(bals)))

		// Don't reject the packet even if it turns out to be bad, downloader will
		// disconnect the peer on its own terms. Simply delivery the BALs to
		// be processed by the caller
		res.Done <- nil

		return hashes, bals, nil
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

	select {
	case <-req.cancel:
		// Sync cycle got cancelled
		log.Trace("Header request cancelled, not reverting", "peer", req.peer, "reqid", req.id)
		return
	default:
	}

	// Remove the request from the tracked set
	delete(b.requests, req.id)

	// Remove the request from the tracked set and mark the task as not-pending,
	// ready for rescheduling
	head := req.head
	if head != b.startHead {
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
	if head != b.startHead {
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
			b.drop(b.scratchOwners[0])
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
		if linked, err = b.extend(nextResp.metas, nextResp.finalizedBlock, nextResp.latestBlock, completed); err != nil {
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
func (d *Downloader) BeaconLightSync(extend beaconSync.BeaconExtendFn, complete func(), start chan *types.Header) error {
	lightSync := newBeaconLightSyncer(d.peers, d.dropPeer, d.quitCh, extend, complete)
	return lightSync.loop(start)
}
