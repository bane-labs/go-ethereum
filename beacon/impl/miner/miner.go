package miner

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
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
}

// Miner is the main object which takes care of submitting new work to consensus
// engine and gathering the sealing result.
type Miner struct {
	mux     *event.TypeMux
	engine  consensus.Engine
	exitCh  chan struct{}
	startCh chan struct{}
	stopCh  chan struct{}

	worker *worker
	rpc    *rpc.Client

	wg sync.WaitGroup
}

func New(eth Backend, rpc *rpc.Client, mux *event.TypeMux, coinbase common.Address) *Miner {
	miner := &Miner{
		mux:     mux,
		engine:  eth.Engine(),
		exitCh:  make(chan struct{}),
		startCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
		worker:  newWorker(eth, rpc, mux, coinbase),
		rpc:     rpc,
	}
	miner.wg.Add(1)
	go miner.update()
	return miner
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

			case downloader.FailedEvent:
				canStart = true
				if shouldStart {
					miner.worker.startMining()
				}
				miner.worker.syncing.Store(false)

			case downloader.DoneEvent:
				canStart = true
				if shouldStart {
					miner.worker.startMining()
				}
				miner.worker.syncing.Store(false)

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
	close(miner.exitCh)
	miner.wg.Wait()
}

func (miner *Miner) Mining() bool {
	return miner.worker.isMining()
}
