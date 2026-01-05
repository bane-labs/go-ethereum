package miner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/params/forks"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	// resultQueueSize is the size of channel listening to sealing result.
	resultQueueSize = 10

	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10

	// staleThreshold is the maximum depth of the acceptable stale block.
	staleThreshold = 0
)

// newWorkReq represents a request for new sealing work submitting.
type newWorkReq struct {
	event     core.ChainHeadEvent
	timestamp uint64
}

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	receipts  []*types.Receipt
	state     *state.StateDB
	block     *types.Block
	createdAt time.Time
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	chainConfig  *params.ChainConfig
	engine       consensus.Engine
	chain        *core.BlockChain
	rpc          *rpc.Client
	feeRecipient common.Address

	// Subscriptions
	mux          *event.TypeMux
	chainHeadCh  chan core.ChainHeadEvent
	chainHeadSub event.Subscription

	// Channels
	newWorkCh chan *newWorkReq
	taskCh    chan *task
	resultCh  chan *types.Block
	startCh   chan struct{}
	exitCh    chan struct{}

	wg sync.WaitGroup

	// Cache indexed by sealing hash
	pendingMu    sync.RWMutex
	pendingTasks map[common.Hash]*task

	// Atomic status counters
	mining  atomic.Bool // The indicator whether the consensus engine is running or not.
	syncing atomic.Bool // The indicator whether the node is still syncing.
}

func newWorker(eth Backend, rpc *rpc.Client, mux *event.TypeMux, feeRecipient common.Address) *worker {
	worker := &worker{
		chainConfig:  eth.BlockChain().Config(),
		engine:       eth.Engine(),
		chain:        eth.BlockChain(),
		rpc:          rpc,
		mux:          mux,
		feeRecipient: feeRecipient,
		chainHeadCh:  make(chan core.ChainHeadEvent, chainHeadChanSize),
		newWorkCh:    make(chan *newWorkReq),
		taskCh:       make(chan *task),
		resultCh:     make(chan *types.Block, resultQueueSize),
		startCh:      make(chan struct{}, 1),
		exitCh:       make(chan struct{}),
		pendingTasks: make(map[common.Hash]*task),
	}

	// Subscribe events for blockchain
	worker.chainHeadSub = eth.BlockChain().SubscribeChainHeadEvent(worker.chainHeadCh)

	worker.wg.Add(4)
	go worker.mainLoop()
	go worker.newWorkLoop()
	go worker.resultLoop()
	go worker.taskLoop()

	return worker
}

// startMining sets the mining status as 1 and triggers new work submitting.
func (w *worker) startMining() {
	w.mining.Store(true)
	w.startCh <- struct{}{}
}

// stopMining sets the mining status as 0.
func (w *worker) stopMining() {
	w.mining.Store(false)
}

// isMining returns an indicator whether worker is mining or not.
func (w *worker) isMining() bool {
	return w.mining.Load()
}

// close terminates all background threads maintained by the worker.
// Note the worker does not support being closed multiple times.
func (w *worker) close() {
	w.mining.Store(false)
	close(w.exitCh)
	w.wg.Wait()
}

// newWorkLoop is a standalone goroutine to submit new sealing work upon received events.
func (w *worker) newWorkLoop() {
	defer w.wg.Done()
	var (
		timestamp uint64 // timestamp for each round of sealing.
	)

	// commit aborts in-flight transaction execution with given signal and resubmits a new one.
	commit := func(event core.ChainHeadEvent) {
		select {
		case w.newWorkCh <- &newWorkReq{event: event, timestamp: timestamp}:
		case <-w.exitCh:
			return
		}
	}
	// clearPending cleans the stale pending tasks.
	clearPending := func(number uint64) {
		w.pendingMu.Lock()
		for h, t := range w.pendingTasks {
			if t.block.NumberU64()+staleThreshold <= number {
				delete(w.pendingTasks, h)
			}
		}
		w.pendingMu.Unlock()
	}

	for {
		select {
		case <-w.startCh: // Trigger a new check manually.
			clearPending(w.chain.CurrentBlock().Number.Uint64())
			timestamp = uint64(time.Now().Unix())
			commit(core.ChainHeadEvent{
				Header: w.chain.CurrentBlock(),
			})

		case head := <-w.chainHeadCh: // A new head from network, commit a miner check.
			clearPending(head.Header.Number.Uint64())
			timestamp = uint64(time.Now().Unix())
			commit(head)

		case <-w.exitCh:
			return
		}
	}
}

// mainLoop is responsible for generating and submitting sealing work based on
// the received event. It can support two modes: automatically generate task and
// submit it or return task according to given parameters for various proposes.
func (w *worker) mainLoop() {
	defer w.wg.Done()
	defer w.chainHeadSub.Unsubscribe()

	for {
		select {
		case req := <-w.newWorkCh:
			w.requestWork(req.event, req.timestamp)

		// System stopped
		case <-w.exitCh:
			return
		case <-w.chainHeadSub.Err():
			return
		}
	}
}

// taskLoop is a standalone goroutine to fetch sealing task from the generator and
// push them to consensus engine.
func (w *worker) taskLoop() {
	defer w.wg.Done()
	for {
		select {
		case task := <-w.taskCh:
			sealHash := w.engine.SealHash(task.block.Header())
			w.pendingMu.Lock()
			w.pendingTasks[sealHash] = task
			w.pendingMu.Unlock()
			if err := w.engine.Seal(w.chain, task.block, w.resultCh); err != nil {
				log.Warn("Block sealing failed", "err", err)
				w.pendingMu.Lock()
				delete(w.pendingTasks, sealHash)
				w.pendingMu.Unlock()
			}
		case <-w.exitCh:
			return
		}
	}
}

// resultLoop is a standalone goroutine to handle sealing result submitting
// and flush relative data to the database.
func (w *worker) resultLoop() {
	defer w.wg.Done()
	for {
		select {
		case block := <-w.resultCh:
			// Short circuit when receiving empty result.
			if block == nil {
				continue
			}
			// Short circuit when receiving duplicate result caused by resubmitting.
			if w.chain.HasBlock(block.Hash(), block.NumberU64()) {
				continue
			}
			var (
				sealhash = w.engine.SealHash(block.Header())
				hash     = block.Hash()
			)
			w.pendingMu.RLock()
			task, exist := w.pendingTasks[sealhash]
			w.pendingMu.RUnlock()
			if !exist {
				log.Error("Block found but no relative pending task", "number", block.Number(), "sealhash", sealhash, "hash", hash)
				continue
			}
			// Different block could share same sealhash, deep copy here to prevent write-write conflict.
			var (
				receipts = make([]*types.Receipt, len(task.receipts))
				logs     []*types.Log
			)
			for i, taskReceipt := range task.receipts {
				receipt := new(types.Receipt)
				receipts[i] = receipt
				*receipt = *taskReceipt

				// add block location fields
				receipt.BlockHash = hash
				receipt.BlockNumber = block.Number()
				receipt.TransactionIndex = uint(i)

				// Update the block hash in all logs since it is now available and not when the
				// receipt/log of individual transactions were created.
				receipt.Logs = make([]*types.Log, len(taskReceipt.Logs))
				for i, taskLog := range taskReceipt.Logs {
					log := new(types.Log)
					receipt.Logs[i] = log
					*log = *taskLog
					log.BlockHash = hash
				}
				logs = append(logs, receipt.Logs...)
			}
			// Commit block and state to database.
			_, err := w.chain.WriteBlockAndSetHead(block, receipts, logs, task.state, true)
			if err != nil {
				log.Error("Failed writing block to chain", "err", err)
				continue
			}
			log.Info("Successfully sealed new block", "number", block.Number(), "sealhash", sealhash, "hash", hash,
				"elapsed", common.PrettyDuration(time.Since(task.createdAt)))

			// Broadcast the block and announce chain insertion event
			w.mux.Post(core.NewMinedBlockEvent{Block: block})

		case <-w.exitCh:
			return
		}
	}
}

// requestWork requests a new sealing task from EL miner through RPC.
func (w *worker) requestWork(event core.ChainHeadEvent, timestamp uint64) {
	// Abort committing if node is still syncing
	if w.syncing.Load() {
		return
	}
	start := time.Now()

	// Find a proper latest header, and use it as parent.
	if event.Header == nil {
		log.Error("Missing parent")
		return
	}

	// Send the latest head info to EL.
	resp, err := w.sendForkChoice(event.Header.Hash(), timestamp)
	if err != nil {
		log.Error("Failed to prepare payload", "err", err)
		return
	}

	// Get the EL payload through RPC request, if there is one.
	if resp.PayloadID != nil {
		payload, err := w.getPayload(resp.PayloadID, timestamp)
		if err != nil {
			log.Error("Failed to fetch payload", "err", err)
			return
		}

		// Rebuild the block from execution payload.
		var versionedHashes []common.Hash
		for _, commitment := range payload.BlobsBundle.Commitments {
			versionedHashes = append(versionedHashes, convertKzgCommitmentToVersionedHash(commitment))
		}
		block, err := engine.ExecutableDataToBlock(*payload.ExecutionPayload, versionedHashes, &types.EmptyRootHash, payload.Requests)
		if err != nil {
			log.Error("Failed to rebuild full block", "err", err)
			return
		}

		// Submit the generated block for consensus sealing.
		err = w.commit(block, start)
		if err != nil {
			log.Error("Failed to commit payload", "err", err)
			return
		}
	}
}

// commit commits new work to consensus engine.
func (w *worker) commit(block *types.Block, start time.Time) error {
	// Execute the transactions of the block to get the state and receipts.
	parent := w.chain.GetHeader(block.Header().ParentHash, block.Header().Number.Uint64()-1)
	parentState, err := w.chain.StateAt(parent.Root)
	if err != nil {
		return err
	}
	state, res, err := w.chain.ProcessState(block, parentState)
	if err != nil {
		return err
	}

	select {
	case w.taskCh <- &task{receipts: res.Receipts, state: state, block: block, createdAt: time.Now()}:
		fees := totalFees(block, res.Receipts)
		feesInEther := new(big.Float).Quo(new(big.Float).SetInt(fees), big.NewFloat(params.Ether))
		log.Info("Commit new sealing work", "number", block.Number(), "sealhash", w.engine.SealHash(block.Header()),
			"txs", len(block.Transactions()), "gas", block.GasUsed(), "fees", feesInEther,
			"elapsed", common.PrettyDuration(time.Since(start)))

	case <-w.exitCh:
		log.Info("Worker has exited")
	}
	return nil
}

// sendForkChoice sends new chain head information to EL miner API through RPC.
func (w *worker) sendForkChoice(headHash common.Hash, timestamp uint64) (engine.ForkChoiceResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	update := engine.ForkchoiceStateV1{
		HeadBlockHash:      headHash,
		SafeBlockHash:      common.Hash{},
		FinalizedBlockHash: common.Hash{},
	}
	attributes := engine.PayloadAttributes{
		Timestamp:             timestamp,
		Random:                common.Hash{},
		SuggestedFeeRecipient: w.feeRecipient,
		Withdrawals:           make([]*types.Withdrawal, 0),
		BeaconRoot:            &types.EmptyRootHash,
	}

	var forkChoiceMethod string
	var resp engine.ForkChoiceResponse
	switch w.chain.Config().LatestFork(timestamp) {
	case forks.Paris:
		forkChoiceMethod = "engine_forkchoiceUpdatedV2"
		attributes.Withdrawals = nil
	case forks.Shanghai:
		forkChoiceMethod = "engine_forkchoiceUpdatedV2"
	case forks.Cancun, forks.Prague:
		forkChoiceMethod = "engine_forkchoiceUpdatedV3"
	default:
		return engine.ForkChoiceResponse{}, fmt.Errorf("fork %s is not supported for engine_forkchoiceUpdated", w.chain.Config().LatestFork(timestamp).String())
	}

	// Set mining attributes only when the worker is set to be mining.
	var err error
	if w.isMining() {
		err = w.rpc.CallContext(ctx, &resp, forkChoiceMethod, update, attributes)
	} else {
		err = w.rpc.CallContext(ctx, &resp, forkChoiceMethod, update, nil)
	}
	if err != nil {
		return engine.ForkChoiceResponse{}, err
	}
	return resp, nil
}

// getPayload requests new block payload from EL miner through RPC.
func (w *worker) getPayload(payloadID *engine.PayloadID, timestamp uint64) (engine.ExecutionPayloadEnvelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var getPayloadMethod string
	var payload engine.ExecutionPayloadEnvelope
	switch w.chain.Config().LatestFork(timestamp) {
	case forks.Paris, forks.Shanghai:
		getPayloadMethod = "engine_getPayloadV2"
	case forks.Cancun:
		getPayloadMethod = "engine_getPayloadV3"
	case forks.Prague:
		getPayloadMethod = "engine_getPayloadV4"
	default:
		return engine.ExecutionPayloadEnvelope{}, fmt.Errorf("fork %s is not supported for engine_getPayload", w.chain.Config().LatestFork(timestamp).String())
	}
	err := w.rpc.CallContext(ctx, &payload, getPayloadMethod, payloadID)
	if err != nil {
		return engine.ExecutionPayloadEnvelope{}, err
	}
	return payload, nil
}

// totalFees computes total consumed miner fees in Wei. Block transactions and receipts have to have the same order.
func totalFees(block *types.Block, receipts []*types.Receipt) *big.Int {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
		// TODO (MariusVanDerWijden) add blob fees
	}
	return feesWei
}

const blobCommitmentVersionKZG uint8 = 0x01

func convertKzgCommitmentToVersionedHash(commitment []byte) common.Hash {
	versionedHash := sha256.Sum256(commitment)
	versionedHash[0] = blobCommitmentVersionKZG
	return versionedHash
}
