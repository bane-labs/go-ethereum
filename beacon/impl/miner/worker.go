package miner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
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
)

// newWorkReq represents a request for new sealing work submitting.
type newWorkReq struct {
	timestamp uint64
}

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	block           *types.Block
	versionedHashes []common.Hash
	requests        [][]byte
	createdAt       time.Time
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	chainConfig  *params.ChainConfig
	engine       consensus.Engine
	chain        *core.BlockChain
	forker       *core.ForkChoice
	rpc          *rpc.Client
	feeRecipient common.Address

	// Subscriptions
	chainHeadCh  chan core.ChainHeadEvent
	chainHeadSub event.Subscription

	// Channels
	newWorkCh chan *newWorkReq
	taskCh    chan *task
	resultCh  chan *types.Block
	startCh   chan struct{}
	exitCh    chan struct{}

	wg sync.WaitGroup

	// Atomic status counters
	mining  atomic.Bool // The indicator whether the consensus engine is running or not.
	syncing atomic.Bool // The indicator whether the node is still syncing.

	// Mutex to prevent parallel fork request.
	forkMu sync.Mutex
}

func newWorker(eth Backend, rpc *rpc.Client, feeRecipient common.Address, shouldPreserve func(header *types.Header) bool) *worker {
	worker := &worker{
		chainConfig:  eth.BlockChain().Config(),
		engine:       eth.Engine(),
		chain:        eth.BlockChain(),
		forker:       core.NewForkChoice(eth.BlockChain(), shouldPreserve),
		rpc:          rpc,
		feeRecipient: feeRecipient,
		chainHeadCh:  make(chan core.ChainHeadEvent, chainHeadChanSize),
		newWorkCh:    make(chan *newWorkReq),
		taskCh:       make(chan *task),
		resultCh:     make(chan *types.Block, resultQueueSize),
		startCh:      make(chan struct{}, 1),
		exitCh:       make(chan struct{}),
	}

	// Subscribe events for blockchain
	worker.chainHeadSub = eth.BlockChain().SubscribeChainHeadEvent(worker.chainHeadCh)

	worker.wg.Add(3)
	go worker.mainLoop()
	go worker.newWorkLoop()
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

	// commit aborts in-flight transaction execution with given signal and resubmits a new one.
	commit := func(timestamp uint64) {
		select {
		case w.newWorkCh <- &newWorkReq{timestamp: timestamp}:
		case <-w.exitCh:
			return
		}
	}

	for {
		select {
		case <-w.startCh: // Trigger a new build manually.
			commit(uint64(time.Now().Unix()))

		case <-w.chainHeadCh: // A new head from network, commit a miner check.
			commit(uint64(time.Now().Unix()))

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
			w.requestWork(req.timestamp)

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
			if err := w.engine.Seal(w.chain, task.block, w.resultCh); err != nil {
				log.Warn("Block sealing failed", "err", err)
			}
		case <-w.exitCh:
			return
		}
	}
}

// requestWork requests a new sealing task from EL miner through RPC.
func (w *worker) requestWork(timestamp uint64) {
	// Abort committing if node is still syncing
	if w.syncing.Load() {
		return
	}
	if !w.isMining() {
		return
	}
	start := time.Now()

	// Lock to ensure EL RW atomicity, no real reorg happens.
	w.forkMu.Lock()
	// Trigger payload build through the fork choice request.
	resp, err := w.sendForkChoice(w.chain.CurrentHeader(), timestamp, true)
	w.forkMu.Unlock()
	if err != nil {
		log.Error("Failed to prepare payload", "err", err)
		return
	}
	if resp.PayloadID == nil {
		log.Error("Missing payload ID")
		return
	}
	payload, err := w.getPayload(resp.PayloadID)
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
	err = w.commit(block, versionedHashes, payload.Requests, start)
	if err != nil {
		log.Error("Failed to commit payload", "err", err)
		return
	}
}

// commit commits new work to consensus engine.
func (w *worker) commit(block *types.Block, versionedHashes []common.Hash, requests [][]byte, start time.Time) error {
	select {
	case w.taskCh <- &task{block: block, versionedHashes: versionedHashes, requests: requests, createdAt: time.Now()}:
		log.Info("Commit new sealing work", "number", block.Number(), "sealhash", w.engine.SealHash(block.Header()),
			"txs", len(block.Transactions()), "gas", block.GasUsed(), "elapsed", common.PrettyDuration(time.Since(start)))

	case <-w.exitCh:
		log.Info("Worker has exited")
	}
	return nil
}

// feedback sends signed block back to EL for execution.
func (w *worker) feedback(block *types.Block) error {
	payload := engine.BlockToExecutableData(block, nil, nil, nil)
	var executionRequests []hexutil.Bytes
	if w.chainConfig.IsPrague(block.Number(), block.Time()) {
		executionRequests = []hexutil.Bytes{}
	}

	// Collect requests.
	_, res, err := w.chain.ProcessState(block, nil)
	if err != nil {
		return err
	}
	for _, v := range res.Requests {
		executionRequests = append(executionRequests, v)
	}

	// Send payload through RPC. TODO: collect and include versioned hashes if possible
	status, err := w.sendPayload(payload.ExecutionPayload, make([]common.Hash, 0), &types.EmptyRootHash, executionRequests, block.Time())
	if err != nil {
		return err
	}
	if status.Status == engine.INVALID {
		return fmt.Errorf("block rejected by EL, err: %v", *status.ValidationError)
	}

	// Lock to ensure EL RW atomicity, no concurrent reorg happens.
	w.forkMu.Lock()
	defer w.forkMu.Unlock()
	// Set head based on reorg check.
	reorg, err := w.forker.ReorgNeeded(w.chain.CurrentHeader(), block.Header())
	if err != nil {
		return err
	}
	if !reorg {
		return nil
	}
	// Send the latest head info to EL.
	resp, err := w.sendForkChoice(block.Header(), block.Time(), false)
	if err != nil {
		return err
	}
	if resp.PayloadStatus.Status != engine.VALID {
		return fmt.Errorf("set head failed, status: %v", resp.PayloadStatus.Status)
	}
	return nil
}

// sendForkChoice sends new chain head information to EL miner API through RPC.
func (w *worker) sendForkChoice(head *types.Header, timestamp uint64, requestMine bool) (engine.ForkChoiceResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	update := engine.ForkchoiceStateV1{
		HeadBlockHash:      head.Hash(),
		SafeBlockHash:      head.ParentHash,
		FinalizedBlockHash: head.ParentHash,
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
	if requestMine {
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
func (w *worker) getPayload(payloadID *engine.PayloadID) (engine.ExecutionPayloadEnvelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var getPayloadMethod string
	var payload engine.ExecutionPayloadEnvelope
	switch payloadID.Version() {
	case engine.PayloadV1, engine.PayloadV2:
		getPayloadMethod = "engine_getPayloadV2"
	case engine.PayloadV3:
		getPayloadMethod = "engine_getPayloadV4"
	default:
		return engine.ExecutionPayloadEnvelope{}, fmt.Errorf("version %v is not supported for engine_getPayload", payloadID.Version())
	}
	err := w.rpc.CallContext(ctx, &payload, getPayloadMethod, payloadID)
	if err != nil {
		return engine.ExecutionPayloadEnvelope{}, err
	}
	return payload, nil
}

// sendPayload sends new block back to EL through RPC.
func (w *worker) sendPayload(payload *engine.ExecutableData, versionedHashes []common.Hash, beaconRoot *common.Hash, executionRequests []hexutil.Bytes, timestamp uint64) (engine.PayloadStatusV1, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var newPayloadMethod string
	var status engine.PayloadStatusV1
	switch w.chain.Config().LatestFork(timestamp) {
	case forks.Paris, forks.Shanghai:
		newPayloadMethod = "engine_newPayloadV2"
	case forks.Cancun:
		newPayloadMethod = "engine_newPayloadV3"
	case forks.Prague:
		newPayloadMethod = "engine_newPayloadV4"
	default:
		return engine.PayloadStatusV1{}, fmt.Errorf("fork %s is not supported for engine_getPayload", w.chain.Config().LatestFork(timestamp).String())
	}
	err := w.rpc.CallContext(ctx, &status, newPayloadMethod, payload, versionedHashes, beaconRoot, executionRequests)
	if err != nil {
		return engine.PayloadStatusV1{}, err
	}
	return status, nil
}

const blobCommitmentVersionKZG uint8 = 0x01

func convertKzgCommitmentToVersionedHash(commitment []byte) common.Hash {
	versionedHash := sha256.Sum256(commitment)
	versionedHash[0] = blobCommitmentVersionKZG
	return versionedHash
}
