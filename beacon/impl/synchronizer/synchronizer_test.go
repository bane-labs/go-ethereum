package synchronizer

import (
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func newCanonical(n int) []*types.Block {
	// Create a database pre-initialize with a genesis block.
	gspec := &core.Genesis{
		Config: params.TestChainConfig,
	}
	_, bs, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), n, nil)
	return append([]*types.Block{gspec.ToBlock()}, bs...)
}

func newTestSynchronizer(chain []*types.Block, complete completeFn) *Synchronizer {
	// Create a test-use synchronizer which use a local chain as the feed.
	lightVerify := func(headers []*types.Header) bool {
		return true
	}
	lightSync := func(extend BeaconExtendFn, start chan *types.Header) error {
		var trustedHeader *types.Header
		var shouldSync atomic.Bool
		for {
			// If the node is going down, unblock.
			select {
			case header, ok := <-start:
				if !ok {
					return nil
				}
				if !shouldSync.Load() {
					trustedHeader = header
					shouldSync.Store(true)
				}
				if trustedHeader == nil || trustedHeader.Number.Uint64() < header.Number.Uint64() {
					trustedHeader = header
				}
			default:
			}
			// Do nothing if should wait.
			if !shouldSync.Load() {
				time.Sleep(time.Second * 3)
				continue
			}
			// Skip the download, mock the result.
			endIndex := trustedHeader.Number.Uint64() + ExpectedHeadersNum
			if endIndex > uint64(len(chain)) {
				endIndex = uint64(len(chain))
			}
			newBlocks := chain[trustedHeader.Number.Uint64():endIndex]
			headers := make([]*types.Header, len(newBlocks))
			metas := make([]common.Hash, len(newBlocks))
			for i := range len(newBlocks) {
				headers[i] = newBlocks[i].Header()
				metas[i] = newBlocks[i].Hash()
			}
			trustedHeader = newBlocks[len(newBlocks)-2].Header()
			if err := extend(headers, metas, newBlocks[len(newBlocks)-2], newBlocks[len(newBlocks)-1]); err != nil {
				shouldSync.Store(false)
				continue
			}
			if len(newBlocks) < ExpectedHeadersNum {
				shouldSync.Store(false)
			}
		}
	}
	return New(chain[0], lightVerify, lightSync, complete)
}

func TestSynchronizerStatic(t *testing.T) {
	chain := newCanonical(2048)
	complete := func(block *types.Block) error {
		t.Logf("chain trust extends to %d", block.NumberU64())
		return nil
	}
	syncer := newTestSynchronizer(chain, complete)
	syncer.Start()
	defer syncer.Stop()
	// Should be able to sync and finish automatically.
	timer := time.NewTimer(time.Second * 3)
	for syncer.trustedHead.NumberU64() < uint64(len(chain)-2) {
		select {
		case <-timer.C:
			t.Fatalf("Failed to sync chain in three seconds")
		default:
		}
	}
	if len(syncer.pendingChains) > 0 {
		t.Fatalf("pending chain number mismatch, got %d, want %d", len(syncer.pendingChains), 0)
	}
}

func TestSynchronizerContinues(t *testing.T) {
	chain := newCanonical(2048)
	complete := func(block *types.Block) error {
		t.Logf("chain trust extends to %d", block.NumberU64())
		return nil
	}
	syncer := newTestSynchronizer(chain, complete)
	syncer.Start()
	defer syncer.Stop()
	// Start from the half, syner should be able to connect the annonced chain.
	for i := len(chain) / 2; i < len(chain)-1; i++ {
		syncer.NotifyNewHead(chain[i])
	}
	timer := time.NewTimer(time.Second * 3)
	for syncer.trustedHead.NumberU64() < uint64(len(chain)-3) {
		select {
		case <-timer.C:
			t.Fatalf("Failed to sync chain in three seconds")
		default:
		}
	}
	// Should continue to execute complete after synced.
	err := syncer.NotifyNewHead(chain[len(chain)-1])
	if err != nil {
		t.Fatal(err)
	}
	if syncer.trustedHead.NumberU64() < uint64(len(chain)-2) {
		t.Fatalf("trusted chain height mismatch, got %d, want %d", syncer.trustedHead.NumberU64(), uint64(len(chain)-2))
	}
	if len(syncer.pendingChains) > 0 {
		t.Fatalf("pending chain number mismatch, got %d, want %d", len(syncer.pendingChains), 0)
	}
}

func TestSynchronizerDisordered(t *testing.T) {
	chain := newCanonical(256)
	complete := func(block *types.Block) error {
		t.Logf("chain trust extends to %d", block.NumberU64())
		return nil
	}
	syncer := newTestSynchronizer(chain[:len(chain)/2+1], complete)
	syncer.Start()
	defer syncer.Stop()
	// Start from the half, but random annonce.
	annBlocks := chain[len(chain)/2 : len(chain)-1]
	rand.Shuffle(len(annBlocks), func(i, j int) {
		annBlocks[i], annBlocks[j] = annBlocks[j], annBlocks[i]
	})
	for _, block := range annBlocks {
		syncer.NotifyNewHead(block)
	}
	timer := time.NewTimer(time.Second * 3)
	for syncer.trustedHead.NumberU64() < uint64(len(chain)-3) {
		select {
		case <-timer.C:
			t.Fatalf("Failed to sync chain in three seconds")
		default:
		}
	}
	// Should continue to execute complete after synced.
	err := syncer.NotifyNewHead(chain[len(chain)-1])
	if err != nil {
		t.Fatal(err)
	}
	if syncer.trustedHead.NumberU64() < uint64(len(chain)-2) {
		t.Fatalf("trusted chain height mismatch, got %d, want %d", syncer.trustedHead.NumberU64(), uint64(len(chain)-2))
	}
	if len(syncer.pendingChains) > 0 {
		t.Fatalf("pending chain number mismatch, got %d, want %d", len(syncer.pendingChains), 0)
	}
}
