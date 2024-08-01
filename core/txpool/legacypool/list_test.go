// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package legacypool

import (
	"container/heap"
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Tests that transactions can be added to strict lists and list contents and
// nonce boundaries are correctly maintained.
func TestStrictListAdd(t *testing.T) {
	// Generate a list of transactions to insert
	key, _ := crypto.GenerateKey()

	txs := make(types.Transactions, 1024)
	for i := 0; i < len(txs); i++ {
		txs[i] = transaction(uint64(i), 0, key)
	}
	// Insert the transactions in a random order
	list := newList(true)
	for _, v := range rand.Perm(len(txs)) {
		list.Add(txs[v], DefaultConfig.PriceBump)
	}
	// Verify internal state
	if len(list.txs.items) != len(txs) {
		t.Errorf("transaction count mismatch: have %d, want %d", len(list.txs.items), len(txs))
	}
	for i, tx := range txs {
		if list.txs.items[tx.Nonce()] != tx {
			t.Errorf("item %d: transaction mismatch: have %v, want %v", i, list.txs.items[tx.Nonce()], tx)
		}
	}
}

func TestPriceHeap(t *testing.T) {
	// Generate a list of transactions to insert
	key, _ := crypto.GenerateKey()
	txs := make(types.Transactions, 20)
	for i := 0; i < 10; i++ {
		txs[i*2] = pricedTransaction(0, 10000, big.NewInt(int64(i+1)), key)
		txs[i*2+1] = pricedToTransaction(0, 10000, big.NewInt(int64(i+1)), key, systemcontracts.GovernanceRewardProxyHash)
	}
	// Insert the transactions in a random order
	pheap := &priceHeap{
		baseFee: big.NewInt(1),
	}
	for _, v := range rand.Perm(len(txs)) {
		heap.Push(pheap, txs[v])
	}
	// Verify order, pop should get non wrapper txs first
	for i := 0; i < len(txs)/2; i++ {
		item := heap.Pop(pheap).(*types.Transaction)
		emptyAddr := common.Address{}
		if *item.To() != emptyAddr {
			t.Errorf("transaction to mismatch: have %s, want %s", *item.To(), emptyAddr)
		}
		if item.GasPrice().Cmp(big.NewInt(int64(i+1))) != 0 {
			t.Errorf("transaction gasPrice mismatch: have %v, want %v", item.GasPrice(), big.NewInt(int64(i+1)))
		}
	}
	for i := 0; i < len(txs)/2; i++ {
		item := heap.Pop(pheap).(*types.Transaction)
		if *item.To() != systemcontracts.GovernanceRewardProxyHash {
			t.Errorf("transaction to mismatch: have %s, want %s", *item.To(), systemcontracts.GovernanceRewardProxyHash)
		}
		if item.GasPrice().Cmp(big.NewInt(int64(i+1))) != 0 {
			t.Errorf("transaction gasPrice mismatch: have %v, want %v", item.GasPrice(), big.NewInt(int64(i+1)))
		}
	}
}

func BenchmarkListAdd(b *testing.B) {
	// Generate a list of transactions to insert
	key, _ := crypto.GenerateKey()

	txs := make(types.Transactions, 100000)
	for i := 0; i < len(txs); i++ {
		txs[i] = transaction(uint64(i), 0, key)
	}
	// Insert the transactions in a random order
	priceLimit := big.NewInt(int64(DefaultConfig.PriceLimit))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list := newList(true)
		for _, v := range rand.Perm(len(txs)) {
			list.Add(txs[v], DefaultConfig.PriceBump)
			list.Filter(priceLimit, DefaultConfig.PriceBump)
		}
	}
}
