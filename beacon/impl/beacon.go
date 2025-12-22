// Package beacon implements minimized Ethereum beacon client.
package impl

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
}

// Beacon is the main object which takes care of submitting new work to consensus
// engine and gathering the sealing result.
type Beacon struct {
	mux     *event.TypeMux
	engine  consensus.Engine
	exitCh  chan struct{}
	startCh chan struct{}
	stopCh  chan struct{}
	worker  *worker
	rpc     *rpc.Client

	wg sync.WaitGroup
}

func New(eth Backend, rpc *rpc.Client, mux *event.TypeMux, engine consensus.Engine, coinbase common.Address) *Beacon {
	beacon := &Beacon{
		mux:     mux,
		engine:  engine,
		exitCh:  make(chan struct{}),
		startCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
		worker:  newWorker(engine, rpc, eth, mux, coinbase, true),
		rpc:     rpc,
	}
	beacon.wg.Add(1)
	go beacon.update()
	return beacon
}

// update keeps track of the downloader events. Please be aware that this is a one shot type of update loop.
// It's entered once and as soon as `Done` or `Failed` has been broadcasted the events are unregistered and
// the loop is exited. This to prevent a major security vuln where external parties can DOS you with blocks
// and halt your mining operation for as long as the DOS continues.
func (beacon *Beacon) update() {
	defer beacon.wg.Done()

	events := beacon.mux.Subscribe(downloader.StartEvent{}, downloader.DoneEvent{}, downloader.FailedEvent{})
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
				wasMining := beacon.Mining()
				beacon.worker.stop()
				canStart = false
				if wasMining {
					// Resume mining after sync was finished
					shouldStart = true
					log.Info("Mining aborted due to sync")
				}
				beacon.worker.syncing.Store(true)

			case downloader.FailedEvent:
				canStart = true
				if shouldStart {
					beacon.worker.start()
				}
				beacon.worker.syncing.Store(false)

			case downloader.DoneEvent:
				canStart = true
				if shouldStart {
					beacon.worker.start()
				}
				beacon.worker.syncing.Store(false)

				// Stop reacting to downloader events
				events.Unsubscribe()
			}
		case <-beacon.startCh:
			if canStart {
				beacon.worker.start()
			}
			shouldStart = true
		case <-beacon.stopCh:
			shouldStart = false
			beacon.worker.stop()
		case <-beacon.exitCh:
			beacon.worker.close()
			return
		}
	}
}

func (beacon *Beacon) Start() {
	beacon.startCh <- struct{}{}
}

func (beacon *Beacon) Stop() {
	beacon.stopCh <- struct{}{}
}

func (beacon *Beacon) Close() {
	close(beacon.exitCh)
	beacon.wg.Wait()
}

func (beacon *Beacon) Mining() bool {
	return beacon.worker.isRunning()
}
