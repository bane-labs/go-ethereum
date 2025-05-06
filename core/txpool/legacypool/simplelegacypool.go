package legacypool

import (
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// SimpleLegacyPool is mainly used for verifying transactions.
type SimpleLegacyPool struct {
	config       Config
	chainconfig  *params.ChainConfig
	chain        BlockChain
	gasTip       atomic.Pointer[uint256.Int]
	txFeed       event.Feed
	reannoTxFeed event.Feed
	signer       types.Signer
	mu           sync.RWMutex

	currentHead   atomic.Pointer[types.Header] // Current head of the blockchain
	currentState  *state.StateDB               // Current state in the blockchain head
	pendingNonces *noncer                      // Pending state tracking virtual nonces

	locals  *accountSet // Set of local transaction to exempt from eviction rules
	journal *journal    // Journal of local transaction to back up to disk

	reserve txpool.AddressReserver       // Address reserver to ensure exclusivity across subpools
	pending map[common.Address]*list     // All currently processable transactions
	cached  map[common.Address]*list     // All currently cached transactions
	queue   map[common.Address]*list     // Queued but non-processable transactions
	beats   map[common.Address]time.Time // Last heartbeat from each known account
	all     *lookup                      // All transactions to allow lookups
	priced  *pricedList                  // All transactions sorted by price

	changesSinceReorg int // A counter for how many drops we've performed in-between reorg.
}

// New creates a new transaction pool to gather, sort and filter inbound
// transactions from the network.
func NewSimple(config Config, chain BlockChain) *SimpleLegacyPool {
	// Sanitize the input to ensure no vulnerable gas prices are set
	config = (&config).sanitize()

	// Create the transaction pool with its initial settings
	pool := &SimpleLegacyPool{
		config:      config,
		chain:       chain,
		chainconfig: chain.Config(),
		signer:      types.LatestSigner(chain.Config()),
		pending:     make(map[common.Address]*list),
		cached:      make(map[common.Address]*list),
		queue:       make(map[common.Address]*list),
		beats:       make(map[common.Address]time.Time),
		all:         newLookup(),
	}
	pool.locals = newAccountSet(pool.signer)
	for _, addr := range config.Locals {
		log.Info("Setting new local account", "address", addr)
		pool.locals.add(addr)
	}
	pool.priced = newPricedList(pool.all)

	if !config.NoLocals && config.Journal != "" {
		pool.journal = newTxJournal(config.Journal)
	}
	return pool
}

// Filter returns whether the given transaction can be consumed by the legacy
// pool, specifically, whether it is a Legacy, AccessList or Dynamic transaction.
func (pool *SimpleLegacyPool) Filter(tx *types.Transaction) bool {
	switch tx.Type() {
	case types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType:
		return true
	default:
		return false
	}
}

// Init sets the gas price needed to keep a transaction in the pool and the chain
// head to allow balance / nonce checks. The transaction journal will be loaded
// from disk and filtered based on the provided starting settings. The internal
// goroutines will be spun up and the pool deemed operational afterwards.
func (pool *SimpleLegacyPool) Init(gasTip uint64, head *types.Header, reserve txpool.AddressReserver) error {
	// Set the address reserver to request exclusive access to pooled accounts
	pool.reserve = reserve

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
	pool.pendingNonces = newNoncer(statedb)

	return nil
}

// CloseSilently is the same as Close(), but doesn't log. Intended to be used
// for short-lived verification pools (not the main node tx pool).
func (pool *SimpleLegacyPool) CloseSilently() {
	if pool.journal != nil {
		pool.journal.close()
	}
}

// Close terminates the transaction pool.
func (pool *SimpleLegacyPool) Close() error {
	pool.CloseSilently()
	log.Info("Transaction pool stopped")
	return nil
}

// Reset implements txpool.SubPool, allowing the legacy pool's internal state to be
// kept in sync with the main transaction pool's internal state.
func (pool *SimpleLegacyPool) Reset(oldHead, newHead *types.Header) {
}

// SubscribeTransactions registers a subscription for new transaction events,
// supporting feeding only newly seen or also resurrected transactions.
func (pool *SimpleLegacyPool) SubscribeTransactions(ch chan<- core.NewTxsEvent, reorgs bool) event.Subscription {
	// The legacy pool has a very messed up internal shuffling, so it's kind of
	// hard to separate newly discovered transaction from resurrected ones. This
	// is because the new txs are added to the queue, resurrected ones too and
	// reorgs run lazily, so separating the two would need a marker.
	return pool.txFeed.Subscribe(ch)
}

// SubscribeReannoTransactions registers a subscription for reannounce transaction events,
// supporting feeding only pending transactions.
func (pool *SimpleLegacyPool) SubscribeReannoTransactions(ch chan<- core.ReannoTxsEvent) event.Subscription {
	return pool.reannoTxFeed.Subscribe(ch)
}

// SetGasTip updates the minimum gas tip required by the transaction pool for a
// new transaction, and drops all transactions below this threshold.
func (pool *SimpleLegacyPool) SetGasTip(tip *big.Int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	var (
		newTip = uint256.MustFromBig(tip)
		old    = pool.gasTip.Load()
	)
	pool.gasTip.Store(newTip)
	// If the min miner fee increased, remove transactions below the new threshold
	if newTip.Cmp(old) > 0 {
		// pool.priced is sorted by GasFeeCap, so we have to iterate through pool.all instead
		drop := pool.all.RemotesBelowTip(tip)
		for _, tx := range drop {
			pool.removeTx(tx.Hash(), false, true)
		}
		pool.priced.Removed(len(drop))
	}
	log.Info("Legacy pool tip threshold updated", "tip", newTip)
}

// Nonce returns the next nonce of an account, with all transactions executable
// by the pool already applied on top.
func (pool *SimpleLegacyPool) Nonce(addr common.Address) uint64 {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	return pool.pendingNonces.get(addr)
}

// GetCachedTransaction returns the cached transaction cached in tx pool.
func (pool *SimpleLegacyPool) GetCachedTransaction(nonce uint64, sender common.Address) *types.Transaction {
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
func (pool *SimpleLegacyPool) Stats() (int, int) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	return pool.stats()
}

// stats retrieves the current pool stats, namely the number of pending and the
// number of queued (non-executable) transactions.
func (pool *SimpleLegacyPool) stats() (int, int) {
	pending := 0
	for _, list := range pool.pending {
		pending += list.Len()
	}
	queued := 0
	for _, list := range pool.queue {
		queued += list.Len()
	}
	return pending, queued
}

// Content retrieves the data content of the transaction pool, returning all the
// pending as well as queued transactions, grouped by account and sorted by nonce.
func (pool *SimpleLegacyPool) Content() (map[common.Address][]*types.Transaction, map[common.Address][]*types.Transaction) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	pending := make(map[common.Address][]*types.Transaction, len(pool.pending))
	for addr, list := range pool.pending {
		pending[addr] = list.Flatten()
	}
	queued := make(map[common.Address][]*types.Transaction, len(pool.queue))
	for addr, list := range pool.queue {
		queued[addr] = list.Flatten()
	}
	return pending, queued
}

// ContentFrom retrieves the data content of the transaction pool, returning the
// pending as well as queued transactions of this address, grouped by nonce.
func (pool *SimpleLegacyPool) ContentFrom(addr common.Address) ([]*types.Transaction, []*types.Transaction) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	var pending []*types.Transaction
	if list, ok := pool.pending[addr]; ok {
		pending = list.Flatten()
	}
	var queued []*types.Transaction
	if list, ok := pool.queue[addr]; ok {
		queued = list.Flatten()
	}
	return pending, queued
}

// Pending retrieves all currently processable transactions, grouped by origin
// account and sorted by nonce.
//
// The transactions can also be pre-filtered by the dynamic fee components to
// reduce allocations and load on downstream subsystems.
func (pool *SimpleLegacyPool) Pending(filter txpool.PendingFilter) map[common.Address][]*txpool.LazyTransaction {
	// If only blob transactions are requested, this pool is unsuitable as it
	// contains none, don't even bother.
	if filter.OnlyBlobTxs {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Convert the new uint256.Int types to the old big.Int ones used by the legacy pool
	var (
		minTipBig  *big.Int
		baseFeeBig *big.Int
	)
	if filter.MinTip != nil {
		minTipBig = filter.MinTip.ToBig()
	}
	if filter.BaseFee != nil {
		baseFeeBig = filter.BaseFee.ToBig()
	}
	pending := make(map[common.Address][]*txpool.LazyTransaction, len(pool.pending))
	for addr, list := range pool.pending {
		txs := list.Flatten()

		// If the miner requests tip enforcement, cap the lists now
		if minTipBig != nil && !pool.locals.contains(addr) {
			for i, tx := range txs {
				if tx.EffectiveGasTipIntCmp(minTipBig, baseFeeBig) < 0 {
					txs = txs[:i]
					break
				}
			}
		}
		if len(txs) > 0 {
			lazies := make([]*txpool.LazyTransaction, len(txs))
			for i := 0; i < len(txs); i++ {
				lazies[i] = &txpool.LazyTransaction{
					Pool:      pool,
					Hash:      txs[i].Hash(),
					Tx:        txs[i],
					Time:      txs[i].Time(),
					GasFeeCap: uint256.MustFromBig(txs[i].GasFeeCap()),
					GasTipCap: uint256.MustFromBig(txs[i].GasTipCap()),
					Gas:       txs[i].Gas(),
					BlobGas:   txs[i].BlobGas(),
				}
			}
			pending[addr] = lazies
		}
	}
	return pending
}

// Locals retrieves the accounts currently considered local by the pool.
func (pool *SimpleLegacyPool) Locals() []common.Address {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	return pool.locals.flatten()
}

// validateTxBasics checks whether a transaction is valid according to the consensus
// rules, but does not check state-dependent validation such as sufficient balance.
// This check is meant as an early check which only needs to be performed once,
// and does not require the pool mutex to be held.
func (pool *SimpleLegacyPool) validateTxBasics(tx *types.Transaction, local bool) error {
	opts := &txpool.ValidationOptions{
		Config: pool.chainconfig,
		Accept: 0 |
			1<<types.LegacyTxType |
			1<<types.AccessListTxType |
			1<<types.DynamicFeeTxType,
		MaxSize: txMaxSize,
		MinTip:  pool.gasTip.Load().ToBig(),
	}
	if local {
		opts.MinTip = new(big.Int)
	}
	if err := txpool.ValidateTransaction(tx, pool.currentHead.Load(), pool.signer, opts); err != nil {
		return err
	}
	return nil
}

// validateTx checks whether a transaction is valid according to the consensus
// rules and adheres to some heuristic limits of the local node (price and size).
func (pool *SimpleLegacyPool) validateTx(tx *types.Transaction, local bool) error {
	opts := &txpool.ValidationOptionsWithState{
		State: pool.currentState,

		FirstNonceGap: nil, // Pool allows arbitrary arrival order, don't invalidate nonce gaps
		UsedAndLeftSlots: func(addr common.Address) (int, int) {
			var have int
			if list := pool.pending[addr]; list != nil {
				have += list.Len()
			}
			if list := pool.queue[addr]; list != nil {
				have += list.Len()
			}
			return have, math.MaxInt
		},
		ExistingExpenditure: func(addr common.Address) *big.Int {
			if list := pool.pending[addr]; list != nil {
				return list.totalcost.ToBig()
			}
			return new(big.Int)
		},
		ExistingCost: func(addr common.Address, nonce uint64) *big.Int {
			if list := pool.pending[addr]; list != nil {
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

// add validates a transaction and inserts it into the non-executable queue for later
// pending promotion and execution. If the transaction is a replacement for an already
// pending or queued one, it overwrites the previous transaction if its price is higher.
//
// If a newly added transaction is marked as local, its sending account will be
// added to the allowlist, preventing any associated transaction from being dropped
// out of the pool due to pricing constraints.
func (pool *SimpleLegacyPool) add(tx *types.Transaction, local bool) (replaced bool, err error) {
	// If the transaction is already known, discard it
	hash := tx.Hash()
	if pool.all.Get(hash) != nil {
		log.Trace("Discarding already known transaction", "hash", hash)
		knownTxMeter.Mark(1)
		return false, txpool.ErrAlreadyKnown
	}
	// Make the local flag. If it's from local source or it's from the network but
	// the sender is marked as local previously, treat it as the local transaction.
	isLocal := local || pool.locals.containsTx(tx)

	// If the transaction fails basic validation, discard it
	if err := pool.validateTx(tx, isLocal); err != nil {
		log.Trace("Discarding invalid transaction", "hash", hash, "err", err)
		invalidTxMeter.Mark(1)
		return false, err
	}
	// already validated by this point
	from, _ := types.Sender(pool.signer, tx)

	// If the address is not yet known, request exclusivity to track the account
	// only by this subpool until all transactions are evicted
	var (
		_, hasPending = pool.pending[from]
		_, hasQueued  = pool.queue[from]
		_, hasCached  = pool.cached[from]
	)
	if !hasPending && !hasQueued && !hasCached {
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
			if err != nil {
				pool.reserve(from, false)
			}
		}()
	}
	// If the transaction pool is full, discard underpriced transactions
	if uint64(pool.all.Slots()+numSlots(tx)) > pool.config.GlobalSlots+pool.config.GlobalSlots+pool.config.GlobalQueue {
		// If the new transaction is underpriced, don't accept it
		if !isLocal && pool.priced.Underpriced(tx) {
			log.Trace("Discarding underpriced transaction", "hash", hash, "gasTipCap", tx.GasTipCap(), "gasFeeCap", tx.GasFeeCap())
			underpricedTxMeter.Mark(1)
			return false, txpool.ErrUnderpriced
		}

		// We're about to replace a transaction. The reorg does a more thorough
		// analysis of what to remove and how, but it runs async. We don't want to
		// do too many replacements between reorg-runs, so we cap the number of
		// replacements to 25% of the slots
		if pool.changesSinceReorg > int(pool.config.GlobalSlots/4) {
			throttleTxMeter.Mark(1)
			return false, ErrTxPoolOverflow
		}

		// New transaction is better than our worse ones, make room for it.
		// If it's a local transaction, forcibly discard all available transactions.
		// Otherwise if we can't make enough room for new one, abort the operation.
		drop, success := pool.priced.Discard(pool.all.Slots()-int(pool.config.GlobalSlots+pool.config.GlobalSlots+pool.config.GlobalQueue)+numSlots(tx), isLocal)

		// Special case, we still can't make the room for the new remote one.
		if !isLocal && !success {
			log.Trace("Discarding overflown transaction", "hash", hash)
			overflowedTxMeter.Mark(1)
			return false, ErrTxPoolOverflow
		}

		// If the new transaction is a future transaction it should never churn pending transactions
		if !isLocal && pool.isGapped(from, tx) {
			var replacesPending bool
			for _, dropTx := range drop {
				dropSender, _ := types.Sender(pool.signer, dropTx)
				if list := pool.pending[dropSender]; list != nil && list.Contains(dropTx.Nonce()) {
					replacesPending = true
					break
				}
			}
			// Add all transactions back to the priced queue
			if replacesPending {
				for _, dropTx := range drop {
					pool.priced.Put(dropTx, false)
				}
				log.Trace("Discarding future transaction replacing pending tx", "hash", hash)
				return false, txpool.ErrFutureReplacePending
			}
		}

		// Kick out the underpriced remote transactions.
		for _, tx := range drop {
			log.Trace("Discarding freshly underpriced transaction", "hash", tx.Hash(), "gasTipCap", tx.GasTipCap(), "gasFeeCap", tx.GasFeeCap())
			underpricedTxMeter.Mark(1)

			sender, _ := types.Sender(pool.signer, tx)
			dropped := pool.removeTx(tx.Hash(), false, sender != from) // Don't unreserve the sender of the tx being added if last from the acc

			pool.changesSinceReorg += dropped
		}
	}

	// If signaturescache config is true and from local, then just cache the tx and return an special error for signature cache
	if pool.config.SignaturesCache && local && !antimev.IsEnvelope(tx) {
		// Check tx ready to process based pending list. The transaction's nonce should be equal to the pending nonce
		pendingNonce := pool.pendingNonces.get(from)
		if tx.Nonce() < pendingNonce {
			return false, fmt.Errorf("%w: next nonce %v, tx nonce %v", core.ErrNonceTooLow, pendingNonce, tx.Nonce())
		} else if tx.Nonce() > pendingNonce {
			return false, fmt.Errorf("%w: next nonce %v, tx nonce %v", core.ErrNonceTooHigh, pendingNonce, tx.Nonce())
		}

		// Try to insert the transaction into the cached queue, we always use new tx if nonce is the same
		if pool.cached[from] == nil {
			pool.cached[from] = newList(true)
		}
		list := pool.cached[from]

		// Nonce already pending, we always use new tx
		inserted, old := list.Replace(tx)
		if !inserted {
			return false, txpool.ErrReplaceUnderpriced
		}
		// New transaction is better, replace old one
		if old != nil {
			pool.all.Remove(old.Hash())
			pool.priced.Removed(1)
			cachedReplaceMeter.Mark(1)
		}
		pool.all.Add(tx, isLocal)
		pool.priced.Put(tx, isLocal)
		log.Trace("Pooled new executable cached transaction", "hash", hash, "from", from, "to", tx.To())

		// Successful promotion, bump the heartbeat
		pool.beats[from] = time.Now()

		// Mark local addresses and journal local transactions
		if local && !pool.locals.contains(from) {
			log.Info("Setting new local account", "address", from)
			pool.locals.add(from)
			pool.priced.Removed(pool.all.RemoteToLocals(pool.locals)) // Migrate the remotes if it's marked as local first time.
		}
		if isLocal {
			localGauge.Inc(1)
		}

		err := ErrTxPoolCached
		log.Info(err.Error(), "txHash", hash, "sender", from, "nonce", tx.Nonce())
		return inserted, err
	}

	// Try to replace an existing transaction in the pending pool
	if list := pool.pending[from]; list != nil && list.Contains(tx.Nonce()) {
		// Nonce already pending, check if required price bump is met
		inserted, old := list.Add(tx, pool.config.PriceBump)
		if !inserted {
			pendingDiscardMeter.Mark(1)
			return false, txpool.ErrReplaceUnderpriced
		}
		// New transaction is better, replace old one
		if old != nil {
			pool.all.Remove(old.Hash())
			pool.priced.Removed(1)
			pendingReplaceMeter.Mark(1)
		}
		pool.all.Add(tx, isLocal)
		pool.priced.Put(tx, isLocal)
		pool.journalTx(from, tx)
		log.Trace("Pooled new executable transaction", "hash", hash, "from", from, "to", tx.To())

		// Successful promotion, bump the heartbeat
		pool.beats[from] = time.Now()
		return old != nil, nil
	}
	// New transaction isn't replacing a pending one, push into queue
	replaced, err = pool.enqueueTx(hash, tx, isLocal, true)
	if err != nil {
		return false, err
	}
	// Mark local addresses and journal local transactions
	if local && !pool.locals.contains(from) {
		log.Info("Setting new local account", "address", from)
		pool.locals.add(from)
		pool.priced.Removed(pool.all.RemoteToLocals(pool.locals)) // Migrate the remotes if it's marked as local first time.
	}
	if isLocal {
		localGauge.Inc(1)
	}
	pool.journalTx(from, tx)

	log.Trace("Pooled new future transaction", "hash", hash, "from", from, "to", tx.To())
	return replaced, nil
}

// isGapped reports whether the given transaction is immediately executable.
func (pool *SimpleLegacyPool) isGapped(from common.Address, tx *types.Transaction) bool {
	// Short circuit if transaction falls within the scope of the pending list
	// or matches the next pending nonce which can be promoted as an executable
	// transaction afterwards. Note, the tx staleness is already checked in
	// 'validateTx' function previously.
	next := pool.pendingNonces.get(from)
	if tx.Nonce() <= next {
		return false
	}
	// The transaction has a nonce gap with pending list, it's only considered
	// as executable if transactions in queue can fill up the nonce gap.
	queue, ok := pool.queue[from]
	if !ok {
		return true
	}
	for nonce := next; nonce < tx.Nonce(); nonce++ {
		if !queue.Contains(nonce) {
			return true // txs in queue can't fill up the nonce gap
		}
	}
	return false
}

// enqueueTx inserts a new transaction into the non-executable transaction queue.
//
// Note, this method assumes the pool lock is held!
func (pool *SimpleLegacyPool) enqueueTx(hash common.Hash, tx *types.Transaction, local bool, addAll bool) (bool, error) {
	// Try to insert the transaction into the future queue
	from, _ := types.Sender(pool.signer, tx) // already validated
	if pool.queue[from] == nil {
		pool.queue[from] = newList(false)
	}
	inserted, old := pool.queue[from].Add(tx, pool.config.PriceBump)
	if !inserted {
		// An older transaction was better, discard this
		queuedDiscardMeter.Mark(1)
		return false, txpool.ErrReplaceUnderpriced
	}
	// Discard any previous transaction and mark this
	if old != nil {
		pool.all.Remove(old.Hash())
		pool.priced.Removed(1)
		queuedReplaceMeter.Mark(1)
	} else {
		// Nothing was replaced, bump the queued counter
		queuedGauge.Inc(1)
	}
	// If the transaction isn't in lookup set but it's expected to be there,
	// show the error log.
	if pool.all.Get(hash) == nil && !addAll {
		log.Error("Missing transaction in lookup set, please report the issue", "hash", hash)
	}
	if addAll {
		pool.all.Add(tx, local)
		pool.priced.Put(tx, local)
	}
	// If we never record the heartbeat, do it right now.
	if _, exist := pool.beats[from]; !exist {
		pool.beats[from] = time.Now()
	}
	return old != nil, nil
}

// journalTx adds the specified transaction to the local disk journal if it is
// deemed to have been sent from a local account.
func (pool *SimpleLegacyPool) journalTx(from common.Address, tx *types.Transaction) {
	// Only journal if it's enabled and the transaction is local
	if pool.journal == nil || !pool.locals.contains(from) {
		return
	}
	if err := pool.journal.insert(tx); err != nil {
		log.Warn("Failed to journal local transaction", "err", err)
	}
}

// Add enqueues a batch of transactions into the pool if they are valid. Depending
// on the local flag, full pricing constraints will or will not be applied.
//
// If sync is set, the method will block until all internal maintenance related
// to the add is finished. Only use this during tests for determinism!
func (pool *SimpleLegacyPool) Add(txs []*types.Transaction, local, sync bool) []error {
	// Do not treat as local if local transactions have been disabled
	local = local && !pool.config.NoLocals

	// Filter out known ones without obtaining the pool lock or recovering signatures
	var (
		errs = make([]error, len(txs))
		news = make([]*types.Transaction, 0, len(txs))
	)
	for i, tx := range txs {
		// If the transaction is known, pre-set the error slot
		if pool.all.Get(tx.Hash()) != nil {
			errs[i] = txpool.ErrAlreadyKnown
			knownTxMeter.Mark(1)
			continue
		}
		// Exclude transactions with basic errors, e.g invalid signatures and
		// insufficient intrinsic gas as soon as possible and cache senders
		// in transactions before obtaining lock
		if err := pool.validateTxBasics(tx, local); err != nil {
			errs[i] = err
			log.Trace("Discarding invalid transaction", "hash", tx.Hash(), "err", err)
			invalidTxMeter.Mark(1)
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
	newErrs, _ := pool.addTxsLocked(news, local)
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
func (pool *SimpleLegacyPool) addTxsLocked(txs []*types.Transaction, local bool) ([]error, *accountSet) {
	dirty := newAccountSet(pool.signer)
	errs := make([]error, len(txs))
	for i, tx := range txs {
		replaced, err := pool.add(tx, local)
		errs[i] = err
		if err == nil && !replaced {
			dirty.addTx(tx)
		}
	}
	validTxMeter.Mark(int64(len(dirty.accounts)))
	return errs, dirty
}

// Status returns the status (unknown/pending/queued) of a batch of transactions
// identified by their hashes.
func (pool *SimpleLegacyPool) Status(hash common.Hash) txpool.TxStatus {
	tx := pool.get(hash)
	if tx == nil {
		return txpool.TxStatusUnknown
	}
	from, _ := types.Sender(pool.signer, tx) // already validated

	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if txList := pool.pending[from]; txList != nil && txList.txs.items[tx.Nonce()] != nil {
		return txpool.TxStatusPending
	} else if txList := pool.queue[from]; txList != nil && txList.txs.items[tx.Nonce()] != nil {
		return txpool.TxStatusQueued
	}
	return txpool.TxStatusUnknown
}

// Get returns a transaction if it is contained in the pool and nil otherwise.
func (pool *SimpleLegacyPool) Get(hash common.Hash) *types.Transaction {
	tx := pool.get(hash)
	if tx == nil {
		return nil
	}
	return tx
}

// get returns a transaction if it is contained in the pool and nil otherwise.
func (pool *SimpleLegacyPool) get(hash common.Hash) *types.Transaction {
	return pool.all.Get(hash)
}

// Has returns an indicator whether txpool has a transaction cached with the
// given hash.
func (pool *SimpleLegacyPool) Has(hash common.Hash) bool {
	return pool.all.Get(hash) != nil
}

// removeTx removes a single transaction from the queue, moving all subsequent
// transactions back to the future queue.
//
// In unreserve is false, the account will not be relinquished to the main txpool
// even if there are no more references to it. This is used to handle a race when
// a tx being added, and it evicts a previously scheduled tx from the same account,
// which could lead to a premature release of the lock.
//
// Returns the number of transactions removed from the pending queue.
func (pool *SimpleLegacyPool) removeTx(hash common.Hash, outofbound bool, unreserve bool) int {
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
				_, hasPending = pool.pending[addr]
				_, hasQueued  = pool.queue[addr]
				_, hasCached  = pool.cached[addr]
			)
			if !hasPending && !hasQueued && !hasCached {
				pool.reserve(addr, false)
			}
		}()
	}
	// Remove it from the list of known transactions
	pool.all.Remove(hash)
	if outofbound {
		pool.priced.Removed(1)
	}
	if pool.locals.contains(addr) {
		localGauge.Dec(1)
	}
	// Remove the transaction from the pending lists and reset the account nonce
	if pending := pool.pending[addr]; pending != nil {
		if removed, invalids := pending.Remove(tx); removed {
			// If no more pending transactions are left, remove the list
			if pending.Empty() {
				delete(pool.pending, addr)
			}
			// Postpone any invalidated transactions
			for _, tx := range invalids {
				// Internal shuffle shouldn't touch the lookup set.
				pool.enqueueTx(tx.Hash(), tx, false, false)
			}
			// Update the account nonce if needed
			pool.pendingNonces.setIfLower(addr, tx.Nonce())
			// Reduce the pending counter
			pendingGauge.Dec(int64(1 + len(invalids)))
			return 1 + len(invalids)
		}
	}
	// Transaction is in the future queue
	if future := pool.queue[addr]; future != nil {
		if removed, _ := future.Remove(tx); removed {
			// Reduce the queued counter
			queuedGauge.Dec(1)
		}
		if future.Empty() {
			delete(pool.queue, addr)
			if pool.cached[addr] == nil || pool.cached[addr].Empty() {
				delete(pool.beats, addr)
			}
		}
	}
	// Remove the transaction from the cached lists
	if cached := pool.cached[addr]; cached != nil {
		if removed, invalids := cached.Remove(tx); removed {
			// If no more cached transactions are left, remove the list
			if cached.Empty() {
				delete(pool.cached, addr)
				if pool.queue[addr] == nil || pool.queue[addr].Empty() {
					delete(pool.beats, addr)
				}
			}
			// Reduce the cached counter
			cachedGauge.Dec(int64(1 + len(invalids)))
			return 1 + len(invalids)
		}
	}
	return 0
}
