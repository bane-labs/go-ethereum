package synchronizer

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
)

const (
	maxPendingChain    = 128  // Max number of pending header chains to keep in memory
	ExpectedHeadersNum = 1024 // The expected number of headers in extending
)

var (
	errInvalidLightHeader   = errors.New("invalid light header")
	errTooManyPendingChains = errors.New("too many pending header chains")
	errExtendStartMismatch  = errors.New("extend start mismatch")
)

// LightVerifyFn is a callback type for light protocal verification.
type LightVerifyFn func(headers []*types.Header) bool

// LightSyncFn is a callback type for finding the latest trusted header with dBFT light client rules.
// Methods implement this should properly return after the start channel is closed.
type LightSyncFn func(extend BeaconExtendFn, start chan *types.Header) error

// BeaconExtendFn is a callback type for extending the trusted header chain with headers received from the network.
type BeaconExtendFn func(verifiedHeaders []*types.Header, metas []common.Hash, finalized *types.Block, latest *types.Block) error

// completeFn is a callback type for synchronizer is synced but still receiving notifications.
type completeFn func(block *types.Block) error

// Choice is a struct to keep track of pending trustful remote heads as potential sync targets.
type Choice struct {
	finalized *types.Block // We suppose n-1 as the finalized block, as we've defined in block insertion.
	latest    *types.Block // The latest received block, but may get reorged by the network.
}

// Synchronizer is responsible for synchronizing the beacon chain with the network.
// Different from the EL downloader, the beacon synchronizer is only resiponsible
// for finding the latest and trusted chain head and announceing it to the EL API,
// to trigger the EL synchronization on the canonical chain.
// NOTE: This implementation only supports one-block reorg.
// TODO: Use database to implement a cache of trusted header, so that we can resume
// the light sync process after restart.
type Synchronizer struct {
	trustedHead   *types.Block           // The block believed to be the latest finalized block
	latestHead    *types.Block           // The block believed to be the latest block
	pendingChains map[common.Hash]Choice // map of pending sync targets, key is the earliest header
	lock          sync.RWMutex           // Mutex to protect the synchronizer state
	syncing       atomic.Bool            // Whether the synchronizer is syncing
	db            ethdb.KeyValueStore    // The database to store the latest trusted head for beacon sync

	startCh chan *types.Header // Channel to signal the synchronizer to start

	lightVerify LightVerifyFn // The verification rule of light protocol
	lightSync   LightSyncFn   // Find the latest header on the trusted chain
	complete    completeFn    // The callback function to use when synchronizer is synced but still receiving notifications
}

// New creates a new synchronizer with the given local head as the trusted header.
func New(localFinalized *types.Block, lightVerify LightVerifyFn, lightSync LightSyncFn, complete completeFn, db ethdb.KeyValueStore) *Synchronizer {
	s := &Synchronizer{
		trustedHead:   localFinalized,
		latestHead:    localFinalized,
		pendingChains: make(map[common.Hash]Choice),
		startCh:       make(chan *types.Header, 1),
		lightVerify:   lightVerify,
		lightSync:     lightSync,
		complete:      complete,
		db:            db,
	}
	// Try to load the latest trusted head for beacon sync from database, in case of restart.
	if trustedHead := rawdb.ReadBeaconSyncTrustedHead(s.db); trustedHead != nil {
		if trustedHead.Number().Cmp(localFinalized.Number()) > 0 {
			s.trustedHead = trustedHead
			s.latestHead = trustedHead
			log.Info("Loaded trusted head from database", "hash", trustedHead.Hash(), "number", trustedHead.NumberU64())
		}
	}
	go s.lightSync(s.BeaconExtend, s.startCh)
	return s
}

// Start starts the initial sync
func (s *Synchronizer) Start() {
	s.startCh <- s.trustedHead.Header()
	s.syncing.Store(true)
}

// Update updates the trusted header, without any verification.
func (s *Synchronizer) Update(newFinalized *types.Block, newLatest *types.Block) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.trustedHead = newFinalized
	s.latestHead = newLatest
	s.finalize()
}

// NotifyNewHead adds a new head to the untrusted chain map, and tries to extend
// the header chain if possible. If the new head block extends the trusted header
// chain, then send it to the EL as sync target.
func (s *Synchronizer) NotifyNewHead(block *types.Block) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	// Shortcut for an old block.
	if block.NumberU64() < s.latestHead.NumberU64() {
		return nil
	}
	// The new head can interact with the light chain, but not verifiable by EL.
	// Then we extend the light chain and anounce safe head to EL.
	if block.NumberU64() == s.latestHead.NumberU64() {
		// Shortcut for an old block.
		if s.latestHead.Hash() == s.trustedHead.Hash() {
			return nil
		}
		// Shortcut for the same block.
		if block.Hash() == s.latestHead.Hash() {
			return nil
		}
		// If the new block reorgs the latest head, then update.
		if !s.lightVerify([]*types.Header{s.trustedHead.Header(), block.Header()}) {
			return errInvalidLightHeader
		}
		if block.Difficulty().Cmp(s.latestHead.Difficulty()) > 0 {
			s.latestHead = block
		}
		return nil
	}
	if block.NumberU64() == s.latestHead.NumberU64()+1 {
		// If the new head extends the light chain, then update the trusted header.
		if block.ParentHash() == s.latestHead.Hash() {
			if !s.lightVerify([]*types.Header{s.latestHead.Header(), block.Header()}) {
				return errInvalidLightHeader
			}
			s.trustedHead = s.latestHead
			s.latestHead = block
			s.finalize()
			return s.complete(s.trustedHead)
		}
	}
	// The new head is also not verifiable by the light chain, then cache it to
	// pending chain choices, and wait for connection when light chain is synced.
	for h, choice := range s.pendingChains {
		// First see if extend, since for a new pending chain, finalize is the latest.
		if block.ParentHash() == choice.latest.Hash() {
			if s.lightVerify([]*types.Header{choice.latest.Header(), block.Header()}) {
				choice.finalized = choice.latest
				choice.latest = block
				s.pendingChains[h] = choice
			}
			s.merge(h)
			return nil
		}
		// Then check if there is reorg or extend on cached chain choices.
		if block.ParentHash() == choice.finalized.Hash() {
			if s.lightVerify([]*types.Header{choice.finalized.Header(), block.Header()}) &&
				block.Difficulty().Cmp(choice.latest.Difficulty()) > 0 {
				choice.latest = block
				s.pendingChains[h] = choice
			}
			s.merge(h)
			return nil
		}
	}
	// If not found, then try add.
	// TODO: implement a better pruning mechanism.
	if len(s.pendingChains) >= maxPendingChain {
		return errTooManyPendingChains
	}
	// Suppose the new block is finalized, if a reorg happen, then there will be another new chain.
	s.pendingChains[block.ParentHash()] = Choice{
		finalized: block,
		latest:    block,
	}
	s.merge(block.ParentHash())
	// If there's no light syncing trying to connect the pending chains, then start one.
	if !s.syncing.Load() {
		s.startCh <- s.trustedHead.Header()
		s.syncing.Store(true)
	}
	return nil
}

// BeaconExtend tries to extend the trusted header chain with the untrusted but verified
// headers received from the network, and returns the new latest trusted header.
func (s *Synchronizer) BeaconExtend(verifiedHeaders []*types.Header, metas []common.Hash, finalized *types.Block, latest *types.Block) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if len(verifiedHeaders) < 2 {
		s.syncing.Store(false)
		return nil
	}
	if verifiedHeaders[0].Hash() != s.trustedHead.Hash() {
		s.syncing.Store(false)
		return errExtendStartMismatch
	}
	// Try to connect pending chains.
	connected := false
	for h, choice := range s.pendingChains {
		if slices.Index(metas, h) > -1 {
			if choice.finalized.NumberU64() > finalized.NumberU64() {
				finalized = choice.finalized
				latest = choice.latest
			}
			connected = true
		}
	}
	s.trustedHead = finalized
	s.latestHead = latest
	s.finalize()
	log.Info("Beacon trust successfully extended", "head", s.trustedHead.Hash(), "number", s.trustedHead.NumberU64())
	// If the trust is extended to the latest, then mark syncing as stopped.
	// The process using this callback can stop as well, but there's no signal.
	// Please take ExpectedHeadersNum as the batch size of light sync.
	if connected || len(verifiedHeaders) < ExpectedHeadersNum {
		s.syncing.Store(false)
		s.complete(s.trustedHead)
	}
	return nil
}

// Stop closes the synchronizer.
func (s *Synchronizer) Stop() {
	close(s.startCh)
}

// Syncing returns whether the synchronizer is currently syncing.
func (s *Synchronizer) Syncing() bool {
	return s.syncing.Load()
}

// merge connects pending chain choices when a latest block changes.
func (s *Synchronizer) merge(key common.Hash) {
	latestHash := s.pendingChains[key].latest.Hash()
	for h, choice := range s.pendingChains {
		// It's possible that this check hits several times, e.g. a reorg block.
		if h == latestHash {
			delete(s.pendingChains, h)
			// Prevent any decreasing.
			if choice.finalized.NumberU64() <= s.pendingChains[key].finalized.NumberU64() {
				continue
			}
			if choice.latest.NumberU64() > choice.finalized.NumberU64() {
				// If the choice has more than 1 blocks.
				s.pendingChains[key] = Choice{
					finalized: choice.finalized,
					latest:    choice.latest,
				}
			} else {
				// If the choice has only 1 block1.
				s.pendingChains[key] = Choice{
					finalized: s.pendingChains[key].latest,
					latest:    choice.latest,
				}
			}
		}
	}
}

// finalize cleans the pending chain choices that are proved invalid,
// and try to connect pending chains when the trust head changes.
func (s *Synchronizer) finalize() {
	for h, choice := range s.pendingChains {
		// If the pending chain's latest block is finalized.
		if choice.latest.Number().Cmp(s.trustedHead.Number()) <= 0 {
			delete(s.pendingChains, h)
		}
		// If the pending chain's paren
		if h == s.latestHead.Hash() {
			delete(s.pendingChains, h)
			if choice.latest.NumberU64() > choice.finalized.NumberU64() {
				// If the choice has more than 1 blocks.
				s.trustedHead = choice.finalized
				s.latestHead = choice.latest
			} else {
				// If the choice has only 1 block.
				s.trustedHead = s.latestHead
				s.latestHead = choice.latest
			}
		}
	}
	rawdb.WriteBeaconSyncTrustedHead(s.db, s.trustedHead)
}
