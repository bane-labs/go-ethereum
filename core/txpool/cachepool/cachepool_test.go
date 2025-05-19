package cachepool

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

var (
	// testTxPoolConfig is a transaction pool configuration without stateful disk
	// sideeffects used during testing.
	testTxPoolConfig Config

	// eip1559Config is a chain config with EIP-1559 enabled at block 0.
	eip1559Config *params.ChainConfig
)

func init() {
	testTxPoolConfig = DefaultConfig

	cpy := *params.TestChainConfig
	eip1559Config = &cpy
	eip1559Config.BerlinBlock = common.Big0
	eip1559Config.LondonBlock = common.Big0
}

type testBlockChain struct {
	config        *params.ChainConfig
	gasLimit      atomic.Uint64
	statedb       *state.StateDB
	chainHeadFeed *event.Feed
}

func newTestBlockChain(config *params.ChainConfig, gasLimit uint64, statedb *state.StateDB, chainHeadFeed *event.Feed) *testBlockChain {
	bc := testBlockChain{config: config, statedb: statedb, chainHeadFeed: new(event.Feed)}
	bc.gasLimit.Store(gasLimit)
	return &bc
}

func (bc *testBlockChain) Config() *params.ChainConfig {
	return bc.config
}

func (bc *testBlockChain) CurrentBlock() *types.Header {
	return &types.Header{
		Number:   new(big.Int),
		GasLimit: bc.gasLimit.Load(),
	}
}

func (bc *testBlockChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	return types.NewBlock(bc.CurrentBlock(), nil, nil, nil, trie.NewStackTrie(nil))
}

func (bc *testBlockChain) StateAt(common.Hash) (*state.StateDB, error) {
	return bc.statedb, nil
}

func (bc *testBlockChain) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
	return bc.chainHeadFeed.Subscribe(ch)
}

func transaction(nonce uint64, gaslimit uint64, key *ecdsa.PrivateKey) *types.Transaction {
	return pricedTransaction(nonce, gaslimit, big.NewInt(1), key)
}

func pricedTransaction(nonce uint64, gaslimit uint64, gasprice *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	tx, _ := types.SignTx(types.NewTransaction(nonce, common.Address{}, big.NewInt(100), gaslimit, gasprice, nil), types.HomesteadSigner{}, key)
	return tx
}

func dynamicFeeTx(nonce uint64, gaslimit uint64, gasFee *big.Int, tip *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	tx, _ := types.SignNewTx(key, types.LatestSignerForChainID(params.TestChainConfig.ChainID), &types.DynamicFeeTx{
		ChainID:    params.TestChainConfig.ChainID,
		Nonce:      nonce,
		GasTipCap:  tip,
		GasFeeCap:  gasFee,
		Gas:        gaslimit,
		To:         &common.Address{},
		Value:      big.NewInt(100),
		Data:       nil,
		AccessList: nil,
	})
	return tx
}

func makeAddressReserver() txpool.AddressReserver {
	var (
		reserved = make(map[common.Address]struct{})
		lock     sync.Mutex
	)
	return func(addr common.Address, reserve bool) error {
		lock.Lock()
		defer lock.Unlock()

		_, exists := reserved[addr]
		if reserve {
			if exists {
				panic("already reserved")
			}
			reserved[addr] = struct{}{}
			return nil
		}
		if !exists {
			panic("not reserved")
		}
		delete(reserved, addr)
		return nil
	}
}

func setupPool() (*CachePool, *ecdsa.PrivateKey) {
	return setupPoolWithConfig(params.TestChainConfig)
}

func setupPoolWithConfig(config *params.ChainConfig) (*CachePool, *ecdsa.PrivateKey) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	blockchain := newTestBlockChain(config, 10000000, statedb, new(event.Feed))

	key, _ := crypto.GenerateKey()
	pool := New(testTxPoolConfig, blockchain)
	if err := pool.Init(1, blockchain.CurrentBlock(), makeAddressReserver()); err != nil {
		panic(err)
	}
	// wait for the pool to initialize
	<-pool.initDoneCh
	return pool, key
}

// validatePoolInternals checks various consistency invariants within the pool.
func validatePoolInternals(pool *CachePool) error {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	// Ensure the total transaction set is consistent with cached
	cached := pool.stats()
	if total := pool.all.Count(); total != cached {
		return fmt.Errorf("total transaction count %d != %d cached", total, cached)
	}
	return nil
}

func deriveSender(tx *types.Transaction) (common.Address, error) {
	return types.Sender(types.HomesteadSigner{}, tx)
}

func testAddBalance(pool *CachePool, addr common.Address, amount *big.Int) {
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, uint256.MustFromBig(amount))
	pool.mu.Unlock()
}

// Test the cache pool
func TestCachePool(t *testing.T) {
	t.Parallel()

	// init pool
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	blockchain := newTestBlockChain(params.TestChainConfig, 1000000, statedb, new(event.Feed))

	config := testTxPoolConfig
	pool := New(config, blockchain)
	pool.Init(1, blockchain.CurrentBlock(), makeAddressReserver())
	defer pool.Close()

	// Create a number of test accounts and fund them (last one will be the local)
	keys := make([]*ecdsa.PrivateKey, 2)
	for i := 0; i < len(keys); i++ {
		keys[i], _ = crypto.GenerateKey()
		testAddBalance(pool, crypto.PubkeyToAddress(keys[i].PublicKey), big.NewInt(10000000))
		fmt.Println(crypto.PubkeyToAddress(keys[i].PublicKey))
	}

	// Generate a batch of transactions from the local account and import them
	txs := make([]*types.Transaction, 3*pool.config.AccountSlots)
	for i := uint64(0); i < 3*pool.config.AccountSlots; i++ {
		gasLimit := uint64(100000)
		if i/pool.config.AccountSlots == 2 {
			gasLimit = 100001
		}
		txs[i] = transaction(i%pool.config.AccountSlots, gasLimit, keys[0])
	}
	errs := pool.addLocals(txs)
	for i, err := range errs {
		switch uint64(i) / pool.config.AccountSlots {
		case 0:
			require.ErrorIs(t, err, ErrTxPoolCached)
		case 1:
			require.ErrorIs(t, err, txpool.ErrAlreadyKnown)
		case 2:
			require.ErrorIs(t, err, ErrTxPoolCached)
		}
	}
	// Check that we only cache processable transaction from account keys[0]
	require.Equal(t, int(pool.config.AccountSlots), pool.all.Count())
	// check we repalce the transaction of index 0 with index 2*pool.config.AccountSlots
	tx := txs[2*pool.config.AccountSlots]
	sender := crypto.PubkeyToAddress(keys[0].PublicKey)
	tx1 := pool.GetCachedTransaction(tx.Nonce(), sender)
	require.NotNil(t, tx1)
	require.Equal(t, tx.Hash(), tx1.Hash())

	// use an other account and dynamicFeeTx, we should cache total 2 signatures
	for i := uint64(0); i < 3*pool.config.AccountSlots; i++ {
		txs[i] = dynamicFeeTx(i, 100000, big.NewInt(1), big.NewInt(1), keys[1])
	}
	pool.addLocals(txs)
	require.Equal(t, int(pool.config.AccountSlots+3*pool.config.AccountSlots), pool.all.Count())
	tx = txs[0]
	sender = crypto.PubkeyToAddress(keys[1].PublicKey)
	tx2 := pool.GetCachedTransaction(tx.Nonce(), sender)
	require.NotNil(t, tx2)
	require.Equal(t, tx.Hash(), tx2.Hash())
}

func testSetNonce(pool *CachePool, addr common.Address, nonce uint64) {
	pool.mu.Lock()
	pool.currentState.SetNonce(addr, nonce)
	pool.mu.Unlock()
}

func TestInvalidTransactions(t *testing.T) {
	t.Parallel()

	pool, key := setupPool()
	defer pool.Close()

	tx := transaction(0, 100, key)
	from, _ := deriveSender(tx)

	// Intrinsic gas too low
	testAddBalance(pool, from, big.NewInt(1))
	if err, want := pool.addLocal(tx), core.ErrIntrinsicGas; !errors.Is(err, want) {
		t.Errorf("want %v have %v", want, err)
	}

	// Insufficient funds
	tx = transaction(0, 100000, key)
	if err, want := pool.addLocal(tx), core.ErrInsufficientFunds; !errors.Is(err, want) {
		t.Errorf("want %v have %v", want, err)
	}

	testSetNonce(pool, from, 1)
	testAddBalance(pool, from, big.NewInt(0xffffffffffffff))
	tx = transaction(0, 100000, key)
	if err, want := pool.addLocal(tx), core.ErrNonceTooLow; !errors.Is(err, want) {
		t.Errorf("want %v have %v", want, err)
	}

	testSetNonce(pool, from, 0)
	if err, want := pool.addLocal(tx), ErrTxPoolCached; !errors.Is(err, want) {
		t.Errorf("want %v have %v", want, err)
	}
}

func TestCache(t *testing.T) {
	t.Parallel()

	pool, key := setupPool()
	defer pool.Close()

	tx := transaction(0, 100000, key)
	from, _ := deriveSender(tx)
	testAddBalance(pool, from, big.NewInt(1000000))
	<-pool.requestReset(nil, nil)

	pool.Add([]*types.Transaction{tx}, true, true)
	if len(pool.cached) != 1 {
		t.Error("expected valid txs to be 1 is", len(pool.cached))
	}

	tx = transaction(1, 100000, key)
	from, _ = deriveSender(tx)
	testSetNonce(pool, from, 2)
	pool.Add([]*types.Transaction{tx}, true, true)

	if _, ok := pool.cached[from].txs.items[tx.Nonce()]; ok {
		t.Error("expected transaction to be in tx pool")
	}
}

func TestNegativeValue(t *testing.T) {
	t.Parallel()

	pool, key := setupPool()
	defer pool.Close()

	tx, _ := types.SignTx(types.NewTransaction(0, common.Address{}, big.NewInt(-1), 100, big.NewInt(1), nil), types.HomesteadSigner{}, key)
	from, _ := deriveSender(tx)
	testAddBalance(pool, from, big.NewInt(1))
	if err := pool.addLocal(tx); err != txpool.ErrNegativeValue {
		t.Error("expected", txpool.ErrNegativeValue, "got", err)
	}
}

func TestTipAboveFeeCap(t *testing.T) {
	t.Parallel()

	pool, key := setupPoolWithConfig(eip1559Config)
	defer pool.Close()

	tx := dynamicFeeTx(0, 100, big.NewInt(1), big.NewInt(2), key)

	if err := pool.addLocal(tx); err != core.ErrTipAboveFeeCap {
		t.Error("expected", core.ErrTipAboveFeeCap, "got", err)
	}
}

func TestVeryHighValues(t *testing.T) {
	t.Parallel()

	pool, key := setupPoolWithConfig(eip1559Config)
	defer pool.Close()

	veryBigNumber := big.NewInt(1)
	veryBigNumber.Lsh(veryBigNumber, 300)

	tx := dynamicFeeTx(0, 100, big.NewInt(1), veryBigNumber, key)
	if err := pool.addLocal(tx); err != core.ErrTipVeryHigh {
		t.Error("expected", core.ErrTipVeryHigh, "got", err)
	}

	tx2 := dynamicFeeTx(0, 100, veryBigNumber, big.NewInt(1), key)
	if err := pool.addLocal(tx2); err != core.ErrFeeCapVeryHigh {
		t.Error("expected", core.ErrFeeCapVeryHigh, "got", err)
	}
}

func TestChainFork(t *testing.T) {
	t.Parallel()

	pool, key := setupPool()
	defer pool.Close()

	addr := crypto.PubkeyToAddress(key.PublicKey)
	resetState := func() {
		statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		statedb.AddBalance(addr, uint256.NewInt(100000000000000))

		pool.chain = newTestBlockChain(pool.chainconfig, 1000000, statedb, new(event.Feed))
		<-pool.requestReset(nil, nil)
	}
	resetState()

	tx := transaction(0, 100000, key)
	if _, err := pool.add(tx); !errors.Is(err, ErrTxPoolCached) {
		t.Error("didn't expect error", err)
	}
	pool.removeTx(tx.Hash(), true, true)

	// reset the pool's internal state
	resetState()
	if _, err := pool.add(tx); !errors.Is(err, ErrTxPoolCached) {
		t.Error("didn't expect error", err)
	}
}

func TestCacheTimeLimiting(t *testing.T) {
	// Reduce the eviction interval to a testable amount
	defer func(old time.Duration) { evictionInterval = old }(evictionInterval)
	evictionInterval = time.Millisecond * 100

	// Create the pool to test the non-expiration enforcement
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	blockchain := newTestBlockChain(params.TestChainConfig, 1000000, statedb, new(event.Feed))

	config := testTxPoolConfig
	config.Lifetime = time.Second

	pool := New(config, blockchain)
	pool.Init(1, blockchain.CurrentBlock(), makeAddressReserver())
	defer pool.Close()

	local, _ := crypto.GenerateKey()

	testAddBalance(pool, crypto.PubkeyToAddress(local.PublicKey), big.NewInt(1000000000))

	if err := pool.addLocal(pricedTransaction(1, 100000, big.NewInt(1), local)); !errors.Is(err, ErrTxPoolCached) {
		t.Fatalf("failed to add local transaction: %v", err)
	}

	cached := pool.stats()
	if cached != 1 {
		t.Fatalf("cached transaction mismatched: have %d, want %d", cached, 1)
	}
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}

	// Allow the eviction interval to run
	time.Sleep(2 * evictionInterval)

	// Transactions should not be evicted from the cached yet since lifetime duration has not passed
	cached = pool.stats()
	if cached != 1 {
		t.Fatalf("cached transaction mismatched: have %d, want %d", cached, 1)
	}
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}

	// Wait a bit for eviction to run and clean up any leftovers
	time.Sleep(2 * config.Lifetime)

	cached = pool.stats()
	if cached != 0 {
		t.Fatalf("cached transactions mismatched: have %d, want %d", cached, 0)
	}

	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}

	// remove current transactions and increase nonce to prepare for a reset and cleanup
	statedb.SetNonce(crypto.PubkeyToAddress(local.PublicKey), 2)
	<-pool.requestReset(nil, nil)

	// make sure cached is cleared
	cached = pool.stats()
	if cached != 0 {
		t.Fatalf("cached transactions mismatched: have %d, want %d", cached, 0)
	}
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}

	// Cached gapped transactions
	if err := pool.addLocal(pricedTransaction(3, 100000, big.NewInt(1), local)); !errors.Is(err, ErrTxPoolCached) {
		t.Fatalf("failed to add local transaction: %v", err)
	}
	time.Sleep(5 * evictionInterval) // A half lifetime pass

	// Cached executable transactions, the life cycle should be restarted.
	if err := pool.addLocal(pricedTransaction(2, 100000, big.NewInt(1), local)); !errors.Is(err, ErrTxPoolCached) {
		t.Fatalf("failed to add local transaction: %v", err)
	}
	time.Sleep(6 * evictionInterval)

	// All gapped transactions shouldn't be kicked out
	cached = pool.stats()
	if cached != 2 {
		t.Fatalf("cached transactions mismatched: have %d, want %d", cached, 0)
	}
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}

	// The whole life time pass after last promotion, kick out stale transactions
	time.Sleep(2 * config.Lifetime)
	cached = pool.stats()
	if cached != 0 {
		t.Fatalf("cached transactions mismatched: have %d, want %d", cached, 0)
	}

	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}
}

// Tests that if transactions start being capped, transactions are also removed from 'all'
func TestCapClearsFromAll(t *testing.T) {
	t.Parallel()

	// Create the pool to test the limit enforcement with
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	blockchain := newTestBlockChain(params.TestChainConfig, 1000000, statedb, new(event.Feed))

	config := testTxPoolConfig
	config.AccountSlots = 2
	config.GlobalSlots = 8

	pool := New(config, blockchain)
	pool.Init(1, blockchain.CurrentBlock(), makeAddressReserver())
	defer pool.Close()

	// Create a number of test accounts and fund them
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)
	testAddBalance(pool, addr, big.NewInt(1000000))

	txs := types.Transactions{}
	for j := 0; j < int(config.GlobalSlots)*2; j++ {
		txs = append(txs, transaction(uint64(j), 100000, key))
	}
	// Import the batch and verify that limits have been enforced
	pool.addLocals(txs)
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}
}

// TestStatusCheck tests that the pool can correctly retrieve the
// cached status of individual transactions.
func TestStatusCheck(t *testing.T) {
	t.Parallel()

	// Create the pool to test the status retrievals with
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	blockchain := newTestBlockChain(params.TestChainConfig, 1000000, statedb, new(event.Feed))

	pool := New(testTxPoolConfig, blockchain)
	pool.Init(1, blockchain.CurrentBlock(), makeAddressReserver())
	defer pool.Close()

	// Create the test accounts to check various transaction statuses with
	keys := make([]*ecdsa.PrivateKey, 3)
	for i := 0; i < len(keys); i++ {
		keys[i], _ = crypto.GenerateKey()
		testAddBalance(pool, crypto.PubkeyToAddress(keys[i].PublicKey), big.NewInt(1000000))
	}
	// Generate and queue a batch of transactions
	txs := types.Transactions{}

	txs = append(txs, pricedTransaction(0, 100000, big.NewInt(1), keys[0]))
	txs = append(txs, pricedTransaction(0, 100000, big.NewInt(1), keys[1]))
	txs = append(txs, pricedTransaction(2, 100000, big.NewInt(1), keys[1]))
	txs = append(txs, pricedTransaction(2, 100000, big.NewInt(1), keys[2]))

	// Import the transaction and ensure they are correctly added
	pool.addLocals(txs)

	cached := pool.stats()
	if cached != 4 {
		t.Fatalf("cached transactions mismatched: have %d, want %d", cached, 4)
	}
	if err := validatePoolInternals(pool); err != nil {
		t.Fatalf("pool internal state corrupted: %v", err)
	}
	// Retrieve the status of each transaction and validate them
	hashes := make([]common.Hash, len(txs))
	for i, tx := range txs {
		hashes[i] = tx.Hash()
	}
	hashes = append(hashes, common.Hash{})
	expect := []txpool.TxStatus{txpool.TxStatusCached, txpool.TxStatusCached, txpool.TxStatusCached, txpool.TxStatusCached, txpool.TxStatusUnknown}

	for i := 0; i < len(hashes); i++ {
		if status := pool.Status(hashes[i]); status != expect[i] {
			t.Errorf("transaction %d: status mismatch: have %v, want %v", i, status, expect[i])
		}
	}
}
