package impl

import (
	"time"

	"github.com/ethereum/go-ethereum/beacon/impl/fetcher"
	"github.com/ethereum/go-ethereum/beacon/impl/miner"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/rpc"
)

type Beacon struct {
	chain        *core.BlockChain
	miner        *miner.Miner
	blockFetcher *fetcher.BlockFetcher
}

// New creates a mock beacon client with basic mining functionality.
func New(eth miner.Backend, rpc *rpc.Client, mux *event.TypeMux, coinbase common.Address) *Beacon {
	return &Beacon{
		chain: eth.BlockChain(),
		miner: miner.New(eth, rpc, mux, coinbase),
	}
}

// StartBlockFetcher enables the block fetching functionality of the beacon client.
func (b *Beacon) StartBlockFetcher(broadcastBlock fetcher.BlockBroadcasterFn, insertChain fetcher.ChainInsertFn,
	dropPeer fetcher.PeerDropFn, fetchHeader fetcher.HeaderRequesterFn, fetchBodies fetcher.BodyRequesterFn) {

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

	b.blockFetcher = fetcher.NewBlockFetcher(b.chain.GetBlockByHash, validator,
		broadcastBlock, heighter, finalizeHeighter, insertChain, dropPeer,
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

// Mining returns whether the beacon client is mining.
func (b *Beacon) Mining() bool {
	return b.miner.Mining()
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
func (b *Beacon) Close() {
	b.miner.Close()
	b.blockFetcher.Stop()
}
