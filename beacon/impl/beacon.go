package impl

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/beacon/impl/fetcher"
	"github.com/ethereum/go-ethereum/beacon/impl/miner"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

type DBFT interface {
	SubscribeNewBlockEvent(ch chan<- *types.Block) event.Subscription
}

type Beacon struct {
	chain          *core.BlockChain
	miner          *miner.Miner
	blockCh        chan *types.Block
	blockFetcher   *fetcher.BlockFetcher
	broadcastBlock fetcher.BlockBroadcasterFn
	wg             sync.WaitGroup
}

// New creates a mock beacon client with basic mining functionality.
func New(eth miner.Backend, rpc *rpc.Client, mux *event.TypeMux, coinbase common.Address, shouldPreserve func(header *types.Header) bool) *Beacon {
	b := &Beacon{
		chain:   eth.BlockChain(),
		miner:   miner.New(eth, rpc, mux, coinbase, shouldPreserve),
		blockCh: make(chan *types.Block),
	}

	b.wg.Add(1)
	go b.minedBroadcastLoop()
	return b
}

// StartBlockFetcher enables the block fetching functionality of the beacon client.
// This is also a part of initialization, to connect to the protocol layer.
func (b *Beacon) StartBlockFetcher(broadcastBlock fetcher.BlockBroadcasterFn, dropPeer fetcher.PeerDropFn,
	fetchHeader fetcher.HeaderRequesterFn, fetchBodies fetcher.BodyRequesterFn) {

	validator := func(header *types.Header) error {
		return b.chain.Engine().VerifyHeader(b.chain, header)
	}
	heighter := func() uint64 {
		return b.chain.CurrentBlock().Number.Uint64()
	}
	finalizeHeighter := func() uint64 {
		fblock := b.chain.CurrentFinalBlock()
		if fblock == nil {
			return 0
		}
		return fblock.Number.Uint64()
	}

	b.broadcastBlock = broadcastBlock
	b.blockFetcher = fetcher.NewBlockFetcher(b.chain.GetBlockByHash, validator,
		broadcastBlock, heighter, finalizeHeighter, b.InsertBlock, dropPeer,
		fetchHeader, fetchBodies)
	b.blockFetcher.Start()
}

// EnqueueBlock sends a received block to beacon for further injection.
func (b *Beacon) EnqueueBlock(peer string, block *types.Block) {
	b.blockFetcher.Enqueue(peer, block)
}

// NotifyBlockAnnon sends a received block announcement to beacon for further download.
func (b *Beacon) NotifyBlockAnnon(peer string, hash common.Hash, number uint64, time time.Time) {
	b.blockFetcher.Notify(peer, hash, number, time)
}

// InsertBlock is a universal block insert function to feed block back to EL.
func (b *Beacon) InsertBlock(block *types.Block) error {
	return b.miner.DispatchBlock(block)
}

// Mining returns whether the beacon client is mining.
func (b *Beacon) Mining() bool {
	return b.miner.Mining()
}

// Syncing returns whether the beacon client is syncing.
func (b *Beacon) Syncing() bool {
	return b.miner.Syncing()
}

func (b *Beacon) SubscribeSyncingEvents(ch chan<- bool) event.Subscription {
	return b.miner.SubscribeSyncingEvents(ch)
}

// StartMining starts beacon mining.
func (b *Beacon) StartMining() {
	b.miner.Start()
}

// StopMining stops beacon mining.
func (b *Beacon) StopMining() {
	b.miner.Stop()
}

// Close closes the beacon client service.
func (b *Beacon) Close() error {
	b.miner.Close()
	b.blockFetcher.Stop()
	close(b.blockCh)
	b.wg.Wait()

	log.Info("Beacon stopped")
	return nil
}

// BlockBroadcaster returns the channel for block broadcasting.
func (b *Beacon) BlockBroadcaster() chan<- *types.Block {
	return b.blockCh
}

// minedBroadcastLoop sends mined blocks to connected peers.
func (b *Beacon) minedBroadcastLoop() {
	defer b.wg.Done()

	for block := range b.blockCh {
		b.broadcastBlock(block, true)  // First propagate block to peers
		b.broadcastBlock(block, false) // Only then announce to the rest
	}
}
