package fetcher

import (
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/beacon"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

const (
	maxQueueLimit = 32 // Maximum number of blocks to fetch ahead of time
	maxRetryCount = 3  // Maximum number of retries for fetching sidecars
)

// SidecarRequesterFn is a callback type for sending a blob sidecar retrieval request.
type SidecarRequesterFn func(string, []common.Hash, chan *beacon.Response) (*beacon.Request, error)

// SidecarDeepRequesterFn is a callback type for deep-fetching blob sidecars.
type SidecarDeepRequesterFn func(blockHash common.Hash) (types.BlobSidecars, error)

// SidecarsAnnouncerFn is a callback type for announcing retrieved blob sidecars to the network.
type SidecarsAnnouncerFn func(blockHash common.Hash)

// blobPeersFn is a callback type to retrieve all known blob peers.
type BlobPeersFn func() []string

// MarkNoBlobPeerFn is a callback type to mark a peer as not having blobs.
type MarkNoBlobPeerFn func(id string)

// fetchingTask represents a scheduled sidecar fetching operation.
type fetchingTask struct {
	block *types.Block // The block to fetch sidecars for
	time  time.Time    // The time of the task created
}

type FileSystem interface {
	HasSidecars(hash common.Hash, indices []int) bool
	InsertBlobsWithoutValidation(header *types.Header, blobs types.BlobSidecars) error
	ShouldRetain(blockNumberRequested *big.Int) bool
	CheckBlobsAvailable(block *types.Block, blobs types.BlobSidecars) error
}

type BlockChain interface {
	CurrentBlock() *types.Header
	GetBlockByHash(hash common.Hash) *types.Block
	GetHeaderByNumber(number uint64) *types.Header
}

// SidecarFetcher is a blob sidecar fetcher, retrieving blob sidecars for blocks
type SidecarFetcher struct {
	done chan common.Hash
	quit chan struct{}

	scheduled map[common.Hash]*fetchingTask // scheduled fetches
	fetching  map[common.Hash]*fetchingTask // currently fetching

	// Callbacks
	announceSidecars SidecarsAnnouncerFn // Announces sidecars to the network
	blobPeers        BlobPeersFn         // Retrieves all known blob peers
	markNoBlobPeer   MarkNoBlobPeerFn    // Marks a peer as not having blobs, to avoid scheduling future fetches to it
	dropPeer         PeerDropFn          // Drops a peer for misbehaving

	fetchSidecars     SidecarRequesterFn     // Fetcher function to retrieve the blob sidecars of a block
	deepFetchSidecars SidecarDeepRequesterFn // Deep-fetcher function to retrieve blob sidecars of a block

	fs    FileSystem
	chain BlockChain
}

// NewSidecarFetcher creates a sidecar fetcher to retrieve blob sidecars.
func NewSidecarFetcher(chain BlockChain, fs FileSystem, blobPeers BlobPeersFn, markNoBlobPeer MarkNoBlobPeerFn, dropPeer PeerDropFn,
	announceSidecars SidecarsAnnouncerFn, fetchSidecars SidecarRequesterFn, deepFetchSidecars SidecarDeepRequesterFn) *SidecarFetcher {
	return &SidecarFetcher{
		done:              make(chan common.Hash),
		quit:              make(chan struct{}),
		scheduled:         make(map[common.Hash]*fetchingTask),
		fetching:          make(map[common.Hash]*fetchingTask),
		blobPeers:         blobPeers,
		markNoBlobPeer:    markNoBlobPeer,
		dropPeer:          dropPeer,
		announceSidecars:  announceSidecars,
		fetchSidecars:     fetchSidecars,
		deepFetchSidecars: deepFetchSidecars,
		fs:                fs,
		chain:             chain,
	}
}

// Start boots up the announcement based synchroniser, accepting and processing
// hash notifications and block fetches until termination requested.
func (f *SidecarFetcher) Start() {
	go f.loop()
}

// Stop terminates the announcement based synchroniser, canceling all pending
// operations.
func (f *SidecarFetcher) Stop() {
	close(f.quit)
}

// Loop is the main fetcher loop, checking and processing various notification
// events.
func (f *SidecarFetcher) loop() {
	// Iterate the sidecar fetching until a quit is requested
	var fetchTimer = time.NewTimer(0)
	<-fetchTimer.C // clear out the channel
	defer fetchTimer.Stop()
	timeoutTicker := time.NewTicker(fetchTimeout)
	defer timeoutTicker.Stop()

	fetchHeight := uint64(0)
	height := f.chain.CurrentBlock().Number.Uint64()
	minRetainBlockNumbers := uint64(params.MinEthEpochsForBlobsSidecarsRequest * params.BlocksPerEthEpoch)
	if height > minRetainBlockNumbers {
		fetchHeight = height - minRetainBlockNumbers
	}

	for {
		// Schedule new sidecar fetches if below the limit
		f.scheduleNextSidecars(&fetchHeight, fetchTimer)

		// Wait for an outside event to occur
		select {
		case <-f.quit:
			// BlockFetcher terminating, abort all operations
			return

		case hash := <-f.done:
			// A pending import finished, remove all traces of the notification
			f.forgetHash(hash)

		case <-timeoutTicker.C:
			// Periodically clean out any abandoned fetches
			earliestHeight := fetchHeight
			for hash, task := range f.fetching {
				cost := time.Since(task.time)
				if cost > maxRetryCount*fetchTimeout {
					f.forgetHash(hash)
					if f.chain.GetBlockByHash(hash) == nil {
						// If a block is found to be missing, then the fetchHeight
						// should be rolled back to the height of the previous block
						// of that particular block. If multiple blocks are missing,
						// the minimum height among them should be taken.
						earliestHeight = min(earliestHeight, task.block.NumberU64()-1)
						continue
					} else if !f.fs.ShouldRetain(task.block.Number()) {
						continue
					}
					// Deep-fetch sidecars if normal fetch failed multiple times
					go func() {
						if _, err := f.deepFetchSidecars(hash); err != nil {
							log.Error("Deep-fetching sidecars failed", "hash", hash, "err", err)
							return
						} else {
							f.done <- hash
							f.announceSidecars(hash)
						}
					}()
				} else if cost > fetchTimeout {
					// Reschedule the fetch
					f.scheduled[hash] = task
				}
			}
			fetchHeight = earliestHeight

		case <-fetchTimer.C:
			// At least one block's timer ran out, check for needing retrieval
			request := make(map[string][]common.Hash)
			reqTaskMap := make(map[string][]*fetchingTask)

			peers := f.blobPeers()
			if len(peers) > 0 {
				for hash, task := range f.scheduled {
					timeout := arriveTimeout - gatherSlack
					if time.Since(task.time) > timeout {
						// Pick a random peer to retrieve from, reset all others
						peer := peers[rand.Intn(len(peers))]
						f.forgetHash(hash)

						if f.fs.HasSidecars(hash, task.block.BlobTxIndices()) {
							// Sidecars are already stored locally, no need to fetch
							continue
						} else {
							request[peer] = append(request[peer], hash)
							reqTaskMap[peer] = append(reqTaskMap[peer], task)
							f.fetching[hash] = task
						}
					}
				}
			}
			// Send out all sidecars requests
			for peer, hashes := range request {
				log.Trace("Fetching scheduled sidecars", "peer", peer, "list", hashes)
				tasks := reqTaskMap[peer]

				go func(peer string, hashes []common.Hash, tasks []*fetchingTask) {
					resCh := make(chan *beacon.Response)

					req, err := f.fetchSidecars(peer, hashes, resCh)
					if err != nil {
						return
					}
					defer req.Close()

					timeout := time.NewTimer(2 * fetchTimeout) // 2x leeway before dropping the peer
					defer timeout.Stop()

					select {
					case res := <-resCh:
						res.Done <- nil
						list := *res.Res.(*beacon.BatchBlobsResponse)
						for i := 0; i < len(list); i++ {
							f.importSidecars(peer, tasks[i].block, list[i])
						}
						if len(list) == 0 {
							f.markNoBlobPeer(peer)
						}

					case <-timeout.C:
						// The peer didn't respond in time. The request
						// was already rescheduled at this point, we were
						// waiting for a catchup. With an unresponsive
						// peer however, it's a protocol violation.
						f.dropPeer(peer)
					}
				}(peer, hashes, tasks)
			}
			// Schedule the next fetch if sidecars are still pending
			f.rescheduleFetch(fetchTimer)
		}
	}
}

// rescheduleFetch resets the specified fetch timer to the next timeout.
func (f *SidecarFetcher) rescheduleFetch(fetch *time.Timer) {
	// Short circuit if no sidecars are being fetched
	if len(f.scheduled) == 0 {
		return
	}
	// Otherwise find the earliest expiring announcement
	earliest := time.Now()
	for _, task := range f.scheduled {
		if earliest.After(task.time) {
			earliest = task.time
		}
	}
	fetch.Reset(arriveTimeout - time.Since(earliest))
}

// importBlocks spawns a new goroutine to run a block insertion into the chain. If the
// block's number is at the same height as the current import phase, it updates
// the phase states accordingly.
func (f *SidecarFetcher) importSidecars(peer string, block *types.Block, sidecars types.BlobSidecars) {
	// Run the import on a new thread
	log.Debug("Importing propagated sidecars", "peer", peer, "number", block.NumberU64(), "hash", block.Hash())
	go func() {
		// Quickly validate the sidecars and propagate the sidecars if it passes
		switch err := f.fs.CheckBlobsAvailable(block, sidecars); err {
		case nil:
			// All ok, quickly propagate to our peers
		default:
			// Something went very wrong, drop the peer
			log.Error("Propagated sidecars verification failed", "peer", peer, "number", block.Number(), "hash", block.Hash(), "err", err)
			f.dropPeer(peer)
			return
		}

		defer func() { f.done <- block.Hash() }()

		// Run the actual import and log any issues
		if err := f.fs.InsertBlobsWithoutValidation(block.Header(), sidecars); err != nil {
			log.Debug("Propagated sidecars import failed", "peer", peer, "number", block.Number(), "hash", block.Hash(), "err", err)
			return
		}
		// If import succeeded, announce the sidecars
		go f.announceSidecars(block.Hash())
	}()
}

// forgetHash removes all traces of a sidecar fetch from the fetcher's
// internal state.
func (f *SidecarFetcher) forgetHash(hash common.Hash) {
	// Remove any pending fetches
	delete(f.scheduled, hash)
	delete(f.fetching, hash)
}

// scheduleNextSidecars schedules new sidecar fetches if below the maximum queue limit.
func (f *SidecarFetcher) scheduleNextSidecars(fetchHeight *uint64, fetchTimer *time.Timer) {
	maxToFetch := maxQueueLimit - len(f.scheduled)
	if maxToFetch > 0 {
		height := f.chain.CurrentBlock().Number.Uint64()
		for h := *fetchHeight + 1; h <= height && maxToFetch > 0; h++ {
			header := f.chain.GetHeaderByNumber(h)
			if header == nil {
				log.Error("Can't fetch sidecars, missing header", "number", h)
				break
			}
			if f.fs.ShouldRetain(header.Number) && header.BlobGasUsed != nil && *header.BlobGasUsed > 0 {
				hash := header.Hash()
				block := f.chain.GetBlockByHash(hash)
				if block == nil {
					log.Error("Can't fetch sidecars, missing block", "number", h, "hash", hash)
					break
				}
				if !f.fs.HasSidecars(hash, block.BlobTxIndices()) {
					f.scheduled[hash] = &fetchingTask{
						block: block,
						time:  time.Now(),
					}
					maxToFetch--
				}
			}
			*fetchHeight++
		}
	}
	if len(f.scheduled) > 0 && len(f.fetching) < maxQueueLimit {
		f.rescheduleFetch(fetchTimer)
	}
}
