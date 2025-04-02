package legacypool

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/prque"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

var (
	// ErrTxPoolCached is returned if the transaction is cached in cache pool successfully.
	// We use an error return here because we don't want a wallet nonce change after response.
	ErrTxPoolCached = errors.New("transaction cached")
	// ErrCachePoolOverflow is returned if the transaction pool is full and can't accept
	// another remote transaction.
	ErrCachePoolOverflow = errors.New("cachepool is full")
)

var (
	// Metrics for the cache pool
	cachedReplaceMeter   = metrics.NewRegisteredMeter("txpool/cached/replace", nil)
	cachedRateLimitMeter = metrics.NewRegisteredMeter("txpool/cached/ratelimit", nil) // Dropped due to rate limiting
	cachedNofundsMeter   = metrics.NewRegisteredMeter("txpool/cached/nofunds", nil)   // Dropped due to out-of-funds
	cachedEvictionMeter  = metrics.NewRegisteredMeter("txpool/cached/eviction", nil)

	// General tx metrics
	knownCachedTxMeter   = metrics.NewRegisteredMeter("txpool/cached/known", nil)
	invalidCachedTxMeter = metrics.NewRegisteredMeter("txpool/cached/invalid", nil)

	// throttleCachedTxMeter counts how many transactions are rejected due to too-many-changes between
	// txpool reorgs.
	throttleCachedTxMeter = metrics.NewRegisteredMeter("txpool/cached/throttle", nil)
	// cachedReorgDurationTimer measures how long time a txpool reorg takes.
	cachedReorgDurationTimer = metrics.NewRegisteredTimer("txpool/cached/reorgtime", nil)
	// cachedDropBetweenReorgHistogram counts how many drops we experience between two reorg runs. It is expected
	// that this number is pretty low, since txpool reorgs happen very frequently.
	cachedDropBetweenReorgHistogram = metrics.NewRegisteredHistogram("txpool/cached/dropbetweenreorg", nil, metrics.NewExpDecaySample(1028, 0.015))

	cachedGauge      = metrics.NewRegisteredGauge("txpool/cached", nil)
	cachedSlotsGauge = metrics.NewRegisteredGauge("txpool/cached/slots", nil)
)

// CacheConfig are the configuration parameters of the transaction pool.
type CacheConfig struct {
	AccountSlots uint64 // Number of cached transaction slots guaranteed per account
	GlobalSlots  uint64 // Maximum number of cached transaction slots for all accounts

	Lifetime time.Duration // Maximum amount of time cached transaction are queued
}

// DefaultConfig contains the default configurations for the transaction pool.
var DefaultCacheConfig = CacheConfig{
	AccountSlots: 16,
	GlobalSlots:  4096 + 1024, // urgent + floating queue capacity with 4:1 ratio

	Lifetime: 3 * time.Hour,
}

// sanitize checks the provided user configurations and changes anything that's
// unreasonable or unworkable.
func (config *CacheConfig) sanitize() CacheConfig {
	conf := *config
	if conf.AccountSlots < 1 {
		log.Warn("Sanitizing invalid txpool account slots", "provided", conf.AccountSlots, "updated", DefaultConfig.AccountSlots)
		conf.AccountSlots = DefaultConfig.AccountSlots
	}
	if conf.GlobalSlots < 1 {
		log.Warn("Sanitizing invalid txpool global slots", "provided", conf.GlobalSlots, "updated", DefaultConfig.GlobalSlots)
		conf.GlobalSlots = DefaultConfig.GlobalSlots
	}
	if conf.Lifetime < 1 {
		log.Warn("Sanitizing invalid txpool lifetime", "provided", conf.Lifetime, "updated", DefaultConfig.Lifetime)
		conf.Lifetime = DefaultConfig.Lifetime
	}
	return conf
}

// CachePool contains all currently AMEV cached transactions.
type CachePool struct {
	config      CacheConfig
	chainconfig *params.ChainConfig
	chain       BlockChain
	gasTip      atomic.Pointer[uint256.Int]
	signer      types.Signer
	mu          sync.RWMutex

	currentHead  atomic.Pointer[types.Header] // Current head of the blockchain
	currentState *state.StateDB               // Current state in the blockchain head

	reserve txpool.AddressReserver       // Address reserver to ensure exclusivity across subpools
	cached  map[common.Address]*list     // All currently cached transactions
	beats   map[common.Address]time.Time // Last heartbeat from each known account
	all     *lookup                      // All transactions to allow lookups

	reqResetCh      chan *txpoolResetRequest
	reorgDoneCh     chan chan struct{}
	reorgShutdownCh chan struct{}  // requests shutdown of scheduleReorgLoop
	wg              sync.WaitGroup // tracks loop, scheduleReorgLoop
	initDoneCh      chan struct{}  // is closed once the pool is initialized (for tests)

	changesSinceReorg int // A counter for how many drops we've performed in-between reorg.
}

// New creates a new transaction pool to gather, sort and filter inbound
// transactions from the network.
func NewCache(config CacheConfig, chain BlockChain) *CachePool {
	// Sanitize the input to ensure no vulnerable gas prices are set
	config = (&config).sanitize()

	// Create the transaction pool with its initial settings
	pool := &CachePool{
		config:          config,
		chain:           chain,
		chainconfig:     chain.Config(),
		signer:          types.LatestSigner(chain.Config()),
		cached:          make(map[common.Address]*list),
		beats:           make(map[common.Address]time.Time),
		all:             newLookup(),
		reqResetCh:      make(chan *txpoolResetRequest),
		reorgDoneCh:     make(chan chan struct{}),
		reorgShutdownCh: make(chan struct{}),
		initDoneCh:      make(chan struct{}),
	}

	return pool
}

// Filter returns whether the given transaction can be consumed by the cache
// pool, specifically, whether it is a Legacy, AccessList or Dynamic transaction.
func (pool *CachePool) Filter(tx *types.Transaction) bool {
	switch tx.Type() {
	case types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType:
		// This system contract KeyManagementProxyHash will send transactions
		// within the program, so it is excluded from consideration here.
		if !antimev.IsEnvelope(tx) && tx.To().Cmp(systemcontracts.KeyManagementProxyHash) != 0 {
			return true
		}
		return false
	default:
		return false
	}
}

// FilterAdd returns whether the given transaction can be consumed by the cache
// pool, specifically, whether it is a Legacy, AccessList or Dynamic transaction.
//
// If you know whether this transaction is local or not, it is recommended to
// use this method for filtering. Currently, it is being used in the txpool.Add
// method.
func (pool *CachePool) FilterAdd(tx *types.Transaction, local bool) bool {
	if local {
		return pool.Filter(tx)
	}
	return false
}

// Init sets the gas price needed to keep a transaction in the pool and the chain
// head to allow balance / nonce checks. The internal goroutines will be spun up
// and the pool deemed operational afterwards.
func (pool *CachePool) Init(gasTip uint64, head *types.Header, reserve txpool.AddressReserver) error {
	// No need for address reserver processing.
	pool.reserve = func(addr common.Address, reserve bool) error { return nil }

	// Set the basic pool parameters
	pool.gasTip.Store(uint256.NewInt(gasTip))

	// Initialize the state with head block, or fallback to empty one in
	// case the head state is not available (might occur when node is not
	// fully synced).
	statedb, err := pool.chain.StateAt(head.Root)
	if err != nil {
		statedb, err = pool.chain.StateAt(types.EmptyRootHash)
	}
	if err != nil {
		return err
	}
	pool.currentHead.Store(head)
	pool.currentState = statedb

	pool.wg.Add(1)
	go pool.scheduleReorgLoop()

	pool.wg.Add(1)
	go pool.loop()
	return nil
}

// loop is the transaction pool's main event loop, waiting for and reacting to
// outside blockchain events as well as for various reporting and transaction
// eviction events.
func (pool *CachePool) loop() {
	defer pool.wg.Done()

	var (
		prevCached int

		// Start the stats reporting and transaction eviction tickers
		report = time.NewTicker(statsReportInterval)
		evict  = time.NewTicker(evictionInterval)
	)
	defer report.Stop()
	defer evict.Stop()

	// Notify tests that the init phase is done
	close(pool.initDoneCh)
	for {
		select {
		// Handle pool shutdown
		case <-pool.reorgShutdownCh:
			return

		// Handle stats reporting ticks
		case <-report.C:
			pool.mu.RLock()
			cached := pool.stats()
			pool.mu.RUnlock()

			if cached != prevCached {
				log.Debug("Transaction pool status report", "cached", cached)
				prevCached = cached
			}

		// Handle inactive account transaction eviction
		case <-evict.C:
			pool.mu.Lock()
			for addr := range pool.cached {
				// Any cached old enough should be removed
				if time.Since(pool.beats[addr]) > pool.config.Lifetime {
					list := pool.cached[addr].Flatten()
					for _, tx := range list {
						pool.removeTx(tx.Hash(), true, true)
					}
					cachedEvictionMeter.Mark(int64(len(list)))
				}
			}
			pool.mu.Unlock()
		}
	}
}

// Close terminates the transaction pool.
func (pool *CachePool) Close() error {
	// Terminate the pool reorger and return
	close(pool.reorgShutdownCh)
	pool.wg.Wait()

	log.Info("AMEV cache pool stopped")
	return nil
}

// Reset implements txpool.SubPool, allowing the cache pool's internal state to be
// kept in sync with the main transaction pool's internal state.
func (pool *CachePool) Reset(oldHead, newHead *types.Header) {
	wait := pool.requestReset(oldHead, newHead)
	<-wait
}

// SubscribeTransactions registers a subscription for new transaction events,
// supporting feeding only newly seen or also resurrected transactions.
func (pool *CachePool) SubscribeTransactions(ch chan<- core.NewTxsEvent, reorgs bool) event.Subscription {
	// Disable SubscribeTransactions
	return nil
}

// SubscribeReannoTransactions registers a subscription for reannounce transaction events,
// supporting feeding only pending transactions.
func (pool *CachePool) SubscribeReannoTransactions(ch chan<- core.ReannoTxsEvent) event.Subscription {
	// Disable SubscribeReannoTransactions
	return nil
}

// SetGasTip updates the minimum gas tip required by the transaction pool for a
// new transaction, and drops all transactions below this threshold.
func (pool *CachePool) SetGasTip(tip *big.Int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	var (
		newTip = uint256.MustFromBig(tip)
	)
	pool.gasTip.Store(newTip)
	log.Info("Cache pool tip threshold updated", "tip", newTip)
}

// Nonce returns the next nonce of an account, with all transactions executable
// by the pool already applied on top.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Nonce(addr common.Address) uint64 {
	return 0
}

// GetCachedTransaction returns the transaction cached in amev pool, so we just return nil here.
func (pool *CachePool) GetCachedTransaction(nonce uint64, sender common.Address) *types.Transaction {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	list := pool.cached[sender]
	if list == nil {
		return nil
	}
	return list.txs.items[nonce]
}

// Stats retrieves the current pool stats, namely the number of pending and the
// number of queued (non-executable) transactions.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Stats() (int, int) {
	return 0, 0
}

// stats retrieves the current pool stats, namely the number of cached transactions.
func (pool *CachePool) stats() int {
	cached := 0
	for _, list := range pool.cached {
		cached += list.Len()
	}
	return cached
}

// Content retrieves the data content of the transaction pool, returning all the
// pending as well as queued transactions, grouped by account and sorted by nonce.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Content() (map[common.Address][]*types.Transaction, map[common.Address][]*types.Transaction) {
	return make(map[common.Address][]*types.Transaction), make(map[common.Address][]*types.Transaction)
}

// ContentFrom retrieves the data content of the transaction pool, returning the
// pending as well as queued transactions of this address, grouped by nonce.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) ContentFrom(addr common.Address) ([]*types.Transaction, []*types.Transaction) {
	return []*types.Transaction{}, []*types.Transaction{}
}

// Pending retrieves all currently processable transactions, grouped by origin
// account and sorted by nonce.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Pending(filter txpool.PendingFilter) map[common.Address][]*txpool.LazyTransaction {
	return make(map[common.Address][]*txpool.LazyTransaction)
}

// ValidateTxBasics checks whether a transaction is valid according to the consensus
// rules, but does not check state-dependent validation such as sufficient balance.
// This check is meant as an early check which only needs to be performed once,
// and does not require the pool mutex to be held.
func (pool *CachePool) ValidateTxBasics(tx *types.Transaction) error {
	opts := &txpool.ValidationOptions{
		Config: pool.chainconfig,
		Accept: 0 |
			1<<types.LegacyTxType |
			1<<types.AccessListTxType |
			1<<types.DynamicFeeTxType,
		MaxSize: txMaxSize,
		MinTip:  pool.gasTip.Load().ToBig(),
	}
	return txpool.ValidateTransaction(tx, pool.currentHead.Load(), pool.signer, opts)
}

// validateTx checks whether a transaction is valid according to the consensus
// rules and adheres to some heuristic limits of the local node (price and size).
func (pool *CachePool) validateTx(tx *types.Transaction) error {
	opts := &txpool.ValidationOptionsWithState{
		State: pool.currentState,

		FirstNonceGap:    nil, // Pool allows arbitrary arrival order, don't invalidate nonce gaps
		UsedAndLeftSlots: nil, // Pool has own mechanism to limit the number of transactions
		ExistingExpenditure: func(addr common.Address) *big.Int {
			if list := pool.cached[addr]; list != nil {
				return list.totalcost.ToBig()
			}
			return new(big.Int)
		},
		ExistingCost: func(addr common.Address, nonce uint64) *big.Int {
			if list := pool.cached[addr]; list != nil {
				if tx := list.txs.Get(nonce); tx != nil {
					return tx.Cost()
				}
			}
			return nil
		},
	}
	if err := txpool.ValidateTransactionWithState(tx, pool.signer, opts); err != nil {
		return err
	}
	return nil
}

// add validates a transaction and inserts it into the cached queue. If the
// transaction is a replacement for an already cached one, it overwrites the
// previous transaction.
func (pool *CachePool) add(tx *types.Transaction) (replaced bool, err error) {
	// If the transaction is already known, discard it
	hash := tx.Hash()
	if pool.all.Get(hash) != nil {
		log.Trace("Discarding already known transaction", "hash", hash)
		knownCachedTxMeter.Mark(1)
		return false, txpool.ErrAlreadyKnown
	}

	// If the transaction fails basic validation, discard it
	if err := pool.validateTx(tx); err != nil {
		log.Trace("Discarding invalid transaction", "hash", hash, "err", err)
		invalidCachedTxMeter.Mark(1)
		return false, err
	}
	// already validated by this point
	from, _ := types.Sender(pool.signer, tx)

	// If the address is not yet known, request exclusivity to track the account
	// only by this subpool until all transactions are evicted
	var (
		_, hasCached = pool.cached[from]
	)
	if !hasCached {
		if err := pool.reserve(from, true); err != nil {
			return false, err
		}
		defer func() {
			// If the transaction is rejected by some post-validation check, remove
			// the lock on the reservation set.
			//
			// Note, `err` here is the named error return, which will be initialized
			// by a return statement before running deferred methods. Take care with
			// removing or subscoping err as it will break this clause.
			//
			// This error "ErrTxPoolCached" indicates successful caching, so it needs to be excluded.
			if err != nil && !errors.Is(err, ErrTxPoolCached) {
				pool.reserve(from, false)
			}
		}()
	}
	// If the cache pool is full, discard underpriced transactions
	if uint64(pool.all.Slots()+numSlots(tx)) > pool.config.GlobalSlots {
		// We're about to replace a transaction. The reorg does a more thorough
		// analysis of what to remove and how, but it runs async. We don't want to
		// do too many replacements between reorg-runs, so we cap the number of
		// replacements to 25% of the slots
		if pool.changesSinceReorg > int(pool.config.GlobalSlots/4) {
			throttleCachedTxMeter.Mark(1)
			return false, ErrCachePoolOverflow
		}
	}

	// Check tx ready to process based cached list.
	nonce := pool.currentState.GetNonce(from)
	if tx.Nonce() < nonce {
		return false, fmt.Errorf("%w: next nonce %v, tx nonce %v", core.ErrNonceTooLow, nonce, tx.Nonce())
	}

	// Try to insert the transaction into the cached queue, we always use new tx if nonce is the same
	if pool.cached[from] == nil {
		pool.cached[from] = newList(false)
	}
	list := pool.cached[from]

	// Nonce already cached, we always use new tx
	inserted, old := list.Replace(tx)
	if !inserted {
		return false, txpool.ErrReplaceUnderpriced
	}

	// New transaction is better, replace old one
	if old != nil {
		pool.all.Remove(old.Hash())
		cachedReplaceMeter.Mark(1)
	} else {
		// Nothing was replaced, bump the queued counter
		cachedGauge.Inc(1)
	}
	pool.all.Add(tx)
	log.Trace("Pooled new executable cached transaction", "hash", hash, "from", from, "to", tx.To())

	// Successful promotion, bump the heartbeat
	pool.beats[from] = time.Now()

	err = ErrTxPoolCached
	log.Info(err.Error(), "txHash", hash, "sender", from, "nonce", tx.Nonce())
	return true, err
}

// Add enqueues a batch of transactions into the pool if they are valid.
//
// If sync is set, the method will block until all internal maintenance related
// to the add is finished. Only use this during tests for determinism!
func (pool *CachePool) Add(txs []*types.Transaction, sync bool) []error {
	// Filter out known ones without obtaining the pool lock or recovering signatures
	var (
		errs = make([]error, len(txs))
		news = make([]*types.Transaction, 0, len(txs))
	)
	for i, tx := range txs {
		// If the transaction is known, pre-set the error slot
		if pool.all.Get(tx.Hash()) != nil {
			errs[i] = txpool.ErrAlreadyKnown
			knownCachedTxMeter.Mark(1)
			continue
		}
		// Exclude transactions with basic errors, e.g invalid signatures and
		// insufficient intrinsic gas as soon as possible and cache senders
		// in transactions before obtaining lock
		if err := pool.ValidateTxBasics(tx); err != nil {
			errs[i] = err
			log.Trace("Discarding invalid transaction", "hash", tx.Hash(), "err", err)
			invalidCachedTxMeter.Mark(1)
			continue
		}
		// Accumulate all unknown transactions for deeper processing
		news = append(news, tx)
	}
	if len(news) == 0 {
		return errs
	}

	// Process all the new transaction and merge any errors into the original slice
	pool.mu.Lock()
	newErrs := pool.addTxsLocked(news)
	pool.mu.Unlock()

	var nilSlot = 0
	for _, err := range newErrs {
		for errs[nilSlot] != nil {
			nilSlot++
		}
		errs[nilSlot] = err
		nilSlot++
	}
	return errs
}

// addTxsLocked attempts to queue a batch of transactions if they are valid.
// The transaction pool lock must be held.
func (pool *CachePool) addTxsLocked(txs []*types.Transaction) []error {
	errs := make([]error, len(txs))
	for i, tx := range txs {
		_, err := pool.add(tx)
		errs[i] = err
	}
	return errs
}

// Status returns the status (unknown/cached) of a batch of transactions
// identified by their hashes.
func (pool *CachePool) Status(hash common.Hash) txpool.TxStatus {
	if pool.all.Get(hash) != nil {
		return txpool.TxStatusCached
	}
	return txpool.TxStatusUnknown
}

// Get returns a transaction if it is contained in the pool and nil otherwise.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Get(hash common.Hash) *types.Transaction {
	return nil
}

// GetRLP returns a RLP-encoded transaction if it is contained in the pool.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) GetRLP(hash common.Hash) []byte {
	return nil
}

// GetMetadata returns the transaction type and transaction size with the
// given transaction hash.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) GetMetadata(hash common.Hash) *txpool.TxMetadata {
	return nil
}

// GetBlobs is not supported by the cache pool, it is just here to
// implement the txpool.SubPool interface.
func (pool *CachePool) GetBlobs(vhashes []common.Hash) ([]*kzg4844.Blob, []*kzg4844.Proof) {
	return nil, nil
}

// Has returns an indicator whether txpool has a transaction cached with the
// given hash.
//
// For the cache pool, this method will return nothing for now.
func (pool *CachePool) Has(hash common.Hash) bool {
	return false
}

// removeTx removes a single transaction from the queue.
//
// In unreserve is false, the account will not be relinquished to the main txpool
// even if there are no more references to it. This is used to handle a race when
// a tx being added, and it evicts a previously scheduled tx from the same account,
// which could lead to a premature release of the lock.
//
// Returns the number of transactions removed from the cached queue.
func (pool *CachePool) removeTx(hash common.Hash, outofbound bool, unreserve bool) int {
	// Fetch the transaction we wish to delete
	tx := pool.all.Get(hash)
	if tx == nil {
		return 0
	}
	addr, _ := types.Sender(pool.signer, tx) // already validated during insertion

	// If after deletion there are no more transactions belonging to this account,
	// relinquish the address reservation. It's a bit convoluted do this, via a
	// defer, but it's safer vs. the many return pathways.
	if unreserve {
		defer func() {
			var (
				_, hasCached = pool.cached[addr]
			)
			if !hasCached {
				pool.reserve(addr, false)
			}
		}()
	}
	// Remove it from the list of known transactions
	pool.all.Remove(hash)
	// Remove the transaction from the cached lists and reset the account nonce
	if cached := pool.cached[addr]; cached != nil {
		if removed, _ := cached.Remove(tx); removed {
			// If no more cached transactions are left, remove the list
			if cached.Empty() {
				delete(pool.cached, addr)
				delete(pool.beats, addr)
			}

			// Reduce the cached counter
			cachedGauge.Dec(1)
			return 1
		}
	}
	return 0
}

// requestReset requests a pool reset to the new head block.
// The returned channel is closed when the reset has occurred.
func (pool *CachePool) requestReset(oldHead *types.Header, newHead *types.Header) chan struct{} {
	select {
	case pool.reqResetCh <- &txpoolResetRequest{oldHead, newHead}:
		return <-pool.reorgDoneCh
	case <-pool.reorgShutdownCh:
		return pool.reorgShutdownCh
	}
}

// scheduleReorgLoop schedules runs of reset. Code above should not call those methods
// directly, but request them being run using requestReset instead.
func (pool *CachePool) scheduleReorgLoop() {
	defer pool.wg.Done()

	var (
		curDone       chan struct{} // non-nil while runReorg is active
		nextDone      = make(chan struct{})
		launchNextRun bool
		reset         *txpoolResetRequest
	)
	for {
		// Launch next background reorg if needed
		if curDone == nil && launchNextRun {
			// Run the background reorg and announcements
			go pool.runReorg(nextDone, reset)

			// Prepare everything for the next round of reorg
			curDone, nextDone = nextDone, make(chan struct{})
			launchNextRun = false

			reset = nil
		}

		select {
		case req := <-pool.reqResetCh:
			// Reset request: update head if request is already pending.
			if reset == nil {
				reset = req
			} else {
				reset.newHead = req.newHead
			}
			launchNextRun = true
			pool.reorgDoneCh <- nextDone

		case <-curDone:
			curDone = nil

		case <-pool.reorgShutdownCh:
			// Wait for current run to finish.
			if curDone != nil {
				<-curDone
			}
			close(nextDone)
			return
		}
	}
}

// runReorg runs reset on behalf of scheduleReorgLoop.
func (pool *CachePool) runReorg(done chan struct{}, reset *txpoolResetRequest) {
	defer func(t0 time.Time) {
		cachedReorgDurationTimer.Update(time.Since(t0))
	}(time.Now())
	defer close(done)

	pool.mu.Lock()
	if reset != nil {
		// Reset from the old head to the new, rescheduling any reorged transactions
		pool.reset(reset.oldHead, reset.newHead)
	}

	// If a new block appeared, validate the pool of cached transactions. This will
	// remove any transaction that has been included in the block or was invalidated
	// because of another transaction (e.g. higher gas price).
	if reset != nil {
		pool.demoteUnexecutables()
	}
	// Ensure pool.cached sizes stay within the configured limits.
	pool.truncateCached()

	cachedDropBetweenReorgHistogram.Update(int64(pool.changesSinceReorg))
	pool.changesSinceReorg = 0 // Reset change counter
	pool.mu.Unlock()

}

// reset retrieves the current state of the blockchain and ensures the content
// of the transaction pool is valid with regard to the chain state.
func (pool *CachePool) reset(oldHead, newHead *types.Header) {
	// Initialize the internal state to the current head
	if newHead == nil {
		newHead = pool.chain.CurrentBlock() // Special case during testing
	}
	statedb, err := pool.chain.StateAt(newHead.Root)
	if err != nil {
		log.Error("Failed to reset txpool state", "err", err)
		return
	}
	pool.currentHead.Store(newHead)
	pool.currentState = statedb
}

// truncateCached removes transactions from the cached queue if the pool is above the
// cached limit. The algorithm tries to reduce transaction counts by an approximately
// equal number for all for accounts with many cached transactions.
func (pool *CachePool) truncateCached() {
	cached := uint64(0)
	for _, list := range pool.cached {
		cached += uint64(list.Len())
	}
	if cached <= pool.config.GlobalSlots {
		return
	}

	cachedBeforeCap := cached
	// Assemble a spam order to penalize large transactors first
	spammers := prque.New[int64, common.Address](nil)
	for addr, list := range pool.cached {
		// Only evict transactions from high rollers
		if uint64(list.Len()) > pool.config.AccountSlots {
			spammers.Push(addr, int64(list.Len()))
		}
	}
	// Gradually drop transactions from offenders
	offenders := []common.Address{}
	for cached > pool.config.GlobalSlots && !spammers.Empty() {
		// Retrieve the next offender if not local address
		offender, _ := spammers.Pop()
		offenders = append(offenders, offender)

		// Equalize balances until all the same or below threshold
		if len(offenders) > 1 {
			// Calculate the equalization threshold for all current offenders
			threshold := pool.cached[offender].Len()

			// Iteratively reduce all offenders until below limit or threshold reached
			for cached > pool.config.GlobalSlots && pool.cached[offenders[len(offenders)-2]].Len() > threshold {
				for i := 0; i < len(offenders)-1; i++ {
					list := pool.cached[offenders[i]]

					caps := list.Cap(list.Len() - 1)
					for _, tx := range caps {
						// Drop the transaction from the global pools too
						hash := tx.Hash()
						pool.all.Remove(hash)

						log.Trace("Removed fairness-exceeding cached transaction", "hash", hash)
					}
					cachedGauge.Dec(int64(len(caps)))
					cached--
				}
			}
		}
	}

	// If still above threshold, reduce to limit or min allowance
	if cached > pool.config.GlobalSlots && len(offenders) > 0 {
		for cached > pool.config.GlobalSlots && uint64(pool.cached[offenders[len(offenders)-1]].Len()) > pool.config.AccountSlots {
			for _, addr := range offenders {
				list := pool.cached[addr]

				caps := list.Cap(list.Len() - 1)
				for _, tx := range caps {
					// Drop the transaction from the global pools too
					hash := tx.Hash()
					pool.all.Remove(hash)

					log.Trace("Removed fairness-exceeding cached transaction", "hash", hash)
				}
				cachedGauge.Dec(int64(len(caps)))
				cached--
			}
		}
	}
	cachedRateLimitMeter.Mark(int64(cachedBeforeCap - cached))
}

// demoteUnexecutables removes invalid and processed transactions from the pools
// cached queue and any subsequent transactions that become unexecutable
// are removed.
func (pool *CachePool) demoteUnexecutables() {
	// Iterate over all accounts and demote any non-executable transactions
	gasLimit := pool.currentHead.Load().GasLimit
	// remove non-executable transactions from cache pool
	for addr, list := range pool.cached {
		nonce := pool.currentState.GetNonce(addr)

		// Drop all transactions that are deemed too old (low nonce)
		olds := list.Forward(nonce)
		for _, tx := range olds {
			hash := tx.Hash()
			pool.all.Remove(hash)
			log.Trace("Removed old cached transaction", "hash", hash)
		}
		// Drop all transactions that are too costly (low balance or out of gas), and queue any invalids back for later
		balance := pool.currentState.GetBalance(addr)
		if pool.cached[addr] != nil {
			balance = balance.Sub(balance, pool.cached[addr].totalcost)
		}
		drops, _ := list.Filter(balance, gasLimit)
		for _, tx := range drops {
			hash := tx.Hash()
			log.Trace("Removed unpayable cached transaction", "hash", hash)
			pool.all.Remove(hash)
		}
		cachedNofundsMeter.Mark(int64(len(drops)))

		cachedGauge.Dec(int64(len(olds) + len(drops)))

		// Delete the entire cached entry if it became empty.
		if list.Empty() {
			delete(pool.cached, addr)
			delete(pool.beats, addr)
			pool.reserve(addr, false)
		}
	}
}

// Clear implements txpool.SubPool
//
// For the cache pool, this method will do nothing for now.
func (pool *CachePool) Clear() {
}
