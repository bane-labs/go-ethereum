package miner

import (
	"errors"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

// Backend wraps all methods required for mining. Only full node is capable
// to offer all the functions here.
type Backend interface {
	BlockChain() *core.BlockChain
	Engine() consensus.Engine

	Synced() bool
}

// Miner is the main object which takes care of submitting new work to consensus
// engine and gathering the sealing result.
type Miner struct {
	mux     *event.TypeMux
	backend Backend
	exitCh  chan struct{}
	startCh chan struct{}
	stopCh  chan struct{}

	worker      *worker
	syncingFeed event.Feed              // Event feed for syncing status changes, a CL notifier as proxy
	scope       event.SubscriptionScope // Subscription scope for miner events

	wg sync.WaitGroup
}

func New(eth Backend, rpc *rpc.Client, mux *event.TypeMux, coinbase common.Address,
	shouldPreserve func(header *types.Header) bool) *Miner {
	miner := &Miner{
		mux:     mux,
		backend: eth,
		exitCh:  make(chan struct{}),
		startCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
		worker:  newWorker(eth, rpc, coinbase, shouldPreserve),
	}
	miner.wg.Add(1)
	go miner.update()
	return miner
}

// DispatchBlock sends back a mined block to EL. This will perform reorg checks if
// required.
func (miner *Miner) DispatchBlock(block *types.Block, reorgCheck bool) error {
	return miner.worker.feedback(block, reorgCheck)
}

// RequestNewPayload triggers a new block building manually.
// It returns an error if the miner is not currently mining.
func (miner *Miner) RequestNewPayload() error {
	if !miner.worker.isMining() {
		return errors.New("working not mining")
	}
	miner.worker.startCh <- struct{}{}
	return nil
}

// update keeps track of the downloader events. Please be aware that this is a one shot type of update loop.
// It's entered once and as soon as `Done` or `Failed` has been broadcasted the events are unregistered and
// the loop is exited. This to prevent a major security vuln where external parties can DOS you with blocks
// and halt your mining operation for as long as the DOS continues.
func (miner *Miner) update() {
	defer miner.wg.Done()

	events := miner.mux.Subscribe(downloader.StartEvent{}, downloader.DoneEvent{}, downloader.FailedEvent{})
	defer func() {
		if !events.Closed() {
			events.Unsubscribe()
		}
	}()

	shouldStart := false
	canStart := true
	dlEventCh := events.Chan()
	for {
		select {
		case ev := <-dlEventCh:
			if ev == nil {
				// Unsubscription done, stop listening
				dlEventCh = nil
				continue
			}
			switch ev.Data.(type) {
			case downloader.StartEvent:
				wasMining := miner.Mining()
				miner.worker.stopMining()
				canStart = false
				if wasMining {
					// Resume mining after sync was finished
					shouldStart = true
					log.Info("Mining aborted due to sync")
				}
				miner.worker.syncing.Store(true)
				miner.syncingFeed.Send(true)

			case downloader.FailedEvent:
				canStart = true
				if shouldStart {
					miner.worker.startMining()
				}
				miner.worker.syncing.Store(false)
				miner.syncingFeed.Send(false)

			case downloader.DoneEvent:
				canStart = true
				if shouldStart {
					miner.worker.startMining()
				}
				miner.worker.syncing.Store(false)
				miner.syncingFeed.Send(false)

				// Stop reacting to downloader events
				events.Unsubscribe()
			}
		case <-miner.startCh:
			if canStart {
				miner.worker.startMining()
			}
			shouldStart = true
		case <-miner.stopCh:
			shouldStart = false
			miner.worker.stopMining()
		case <-miner.exitCh:
			miner.worker.close()
			return
		}
	}
}

func (miner *Miner) Start() {
	miner.startCh <- struct{}{}
}

func (miner *Miner) Stop() {
	miner.stopCh <- struct{}{}
}

func (miner *Miner) Close() {
	miner.scope.Close()
	close(miner.exitCh)
	miner.wg.Wait()
}

// Mining returns whether the miner is currently mining.
func (miner *Miner) Mining() bool {
	return miner.worker.isMining()
}

// Syncing returns whether the miner is currently syncing.
func (miner *Miner) Syncing() bool {
	return miner.worker.syncing.Load()
}

// SubscribeSyncingEvents subscribes to syncing status changes, should only be used in CL.
func (miner *Miner) SubscribeSyncingEvents(ch chan<- bool) event.Subscription {
	return miner.scope.Track(miner.syncingFeed.Subscribe(ch))
}
