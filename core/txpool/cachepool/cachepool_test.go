package cachepool

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

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
)

func init() {
	testTxPoolConfig = DefaultConfig
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
