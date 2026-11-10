package miner

import (
	"context"
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
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
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

	// transactionCacheSize is the max size of pending transaction cache.
	transactionCacheSize = 512
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
	ctx          context.Context
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
	cancel    context.CancelFunc

	wg sync.WaitGroup

	// Atomic status counters
	mining  atomic.Bool // The indicator whether the consensus engine is running or not.
	syncing atomic.Bool // The indicator whether the node is still syncing.

	// Mutex to prevent parallel fork request.
	forkMu              sync.RWMutex
	pendingPayloadID    *engine.PayloadID
	pendingTransactions types.Transactions

	// Blob management
	blobCache *blobCache
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
		blobCache:    newBlobCache(32),
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

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
	w.cancel()
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
	defer w.forkMu.Unlock()

	// Trigger payload build through the fork choice request, or use the existed ID.
	if w.pendingPayloadID == nil {
		resp, err := w.sendForkChoice(w.chain.CurrentHeader(), timestamp, true)
		if err != nil {
			log.Error("Failed to prepare payload", "err", err)
			return
		}
		if resp.PayloadID == nil {
			log.Error("Missing payload ID")
			return
		}
		w.pendingPayloadID = resp.PayloadID
	}
	payload, err := w.getPayload(w.pendingPayloadID)
	if err != nil {
		log.Error("Failed to fetch payload", "err", err)
		return
	}
	w.pendingPayloadID = nil

	// Rebuild the block from execution payload.
	var versionedHashes []common.Hash
	if payload.BlobsBundle != nil {
		for _, commitment := range payload.BlobsBundle.Commitments {
			versionedHashes = append(versionedHashes, convertKzgCommitmentToVersionedHash(commitment))
		}
	}
	var beaconRoot *common.Hash
	if fork := w.chain.Config().LatestFork(timestamp); fork >= forks.Cancun {
		beaconRoot = &types.EmptyRootHash
	}
	block, err := engine.ExecutableDataToBlock(*payload.ExecutionPayload, versionedHashes, beaconRoot, payload.Requests)
	if err != nil {
		log.Error("Failed to rebuild full block", "err", err)
		return
	}

	// Cache the new block's transactions for potential future use, e.g. for BFT missing transaction request.
	w.pendingTransactions = block.Transactions()
	if payload.BlobsBundle != nil && len(versionedHashes) > 0 {
		bundle := payload.BlobsBundle
		if len(bundle.Proofs) == len(bundle.Blobs) {
			// If we receive V0 blobs, we convert them to V1 and compute the cell proofs, since we don't support V0 anymore.
			bundle.Proofs = make([]hexutil.Bytes, 0, len(bundle.Blobs)*kzg4844.CellProofsPerBlob)
			for i := range bundle.Blobs {
				var blob kzg4844.Blob
				copy(blob[:], bundle.Blobs[i])
				cellProofs, err := kzg4844.ComputeCellProofs(&blob)
				if err != nil {
					log.Error("Failed to compute cell proofs", "err", err)
					w.pendingTransactions = nil
					return
				}
				for n := range cellProofs {
					bundle.Proofs = append(bundle.Proofs, cellProofs[n][:])
				}
			}
		} else if len(bundle.Proofs) != len(bundle.Blobs)*kzg4844.CellProofsPerBlob {
			log.Error("Invalid number of proofs in the blob bundle",
				"expectedV0", len(bundle.Blobs),
				"expectedV1", len(bundle.Blobs)*kzg4844.CellProofsPerBlob,
				"actual", len(bundle.Proofs))
			w.pendingTransactions = nil
			return
		}
		w.blobCache.Add(versionedHashes, bundle)
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
			"txs", len(block.Transactions()), "withdrawals", len(block.Withdrawals()), "gas", block.GasUsed(),
			"elapsed", common.PrettyDuration(time.Since(start)))

	case <-w.exitCh:
		log.Info("Worker has exited")
	}
	return nil
}

// getTransaction tries to find a transaction from the pending transaction cache.
func (w *worker) getTransaction(hash common.Hash) *types.Transaction {
	// The pending payload is also protected by the fork mutex, here we borrow it.
	w.forkMu.RLock()
	defer w.forkMu.RUnlock()

	for _, tx := range w.pendingTransactions {
		if tx.Hash() == hash {
			if tx.Type() == types.BlobTxType {
				// For blob transaction, we need to rebuild the blob sidecar.
				blobHashes := tx.BlobHashes()
				blobs := make([]kzg4844.Blob, len(blobHashes))
				commitments := make([]kzg4844.Commitment, len(blobHashes))
				proofs := make([]kzg4844.Proof, len(blobHashes)*kzg4844.CellProofsPerBlob)
				for i, blobHash := range blobHashes {
					blob, commitment, proof, ok := w.blobCache.Get(blobHash)
					if !ok {
						log.Error("Blob is missing in the cache", "txhash", hash, "blobhash", blobHash)
						return nil
					}
					copy(blobs[i][:], blob)
					copy(commitments[i][:], commitment)
					for n := 0; n < kzg4844.CellProofsPerBlob; n++ {
						copy(proofs[i*kzg4844.CellProofsPerBlob+n][:], proof[n])
					}
				}
				return tx.WithBlobTxSidecar(types.NewBlobTxSidecar(types.BlobSidecarVersion1, blobs, commitments, proofs))
			} else {
				return tx
			}
		}
	}
	return nil
}

// cacheTransactions adds transactions to the pending transaction cache, so mark as
// seen during this round of consensus.
func (w *worker) cacheTransactions(txs []*types.Transaction) {
	w.forkMu.Lock()
	defer w.forkMu.Unlock()
	// Build a map from transaction hash to index for O(1) lookups.
	hashToIndex := make(map[common.Hash]int, len(w.pendingTransactions))
	for idx, tx := range w.pendingTransactions {
		hashToIndex[tx.Hash()] = idx
	}
	// If the transaction is already in the cache, skip it.
	for _, tx := range txs {
		if _, exists := hashToIndex[tx.Hash()]; exists {
			continue
		}
		// Ignore remote transaction if the cache is full.
		if len(w.pendingTransactions) >= transactionCacheSize {
			return
		}
		// Ignore blob transaction if the sidecar is invalid.
		if tx.Type() == types.BlobTxType {
			sidecar := tx.BlobTxSidecar()
			if sidecar == nil {
				continue
			}
			if tx.BlobTxSidecar().Version < types.BlobSidecarVersion1 {
				continue
			}
			if err := core.ValidateBlobSidecar(sidecar, tx.BlobHashes()); err != nil {
				continue
			}
			w.pendingTransactions = append(w.pendingTransactions, tx.WithoutBlobTxSidecar())
			hashToIndex[tx.Hash()] = len(w.pendingTransactions) - 1
			bundle := engine.BlobsBundle{
				Blobs:       make([]hexutil.Bytes, len(sidecar.Blobs)),
				Commitments: make([]hexutil.Bytes, len(sidecar.Commitments)),
				Proofs:      make([]hexutil.Bytes, len(sidecar.Proofs)),
			}
			for i := range sidecar.Blobs {
				bundle.Blobs[i] = append(bundle.Blobs[i], sidecar.Blobs[i][:]...)
				bundle.Commitments[i] = append(bundle.Commitments[i], sidecar.Commitments[i][:]...)
			}
			for i := range sidecar.Proofs {
				bundle.Proofs[i] = append(bundle.Proofs[i], sidecar.Proofs[i][:]...)
			}
			w.blobCache.Add(tx.BlobHashes(), &bundle)
		}
	}
}

// hasBlobs checks if the pending cache has all the blobs for the given hashes.
func (w *worker) hasBlobs(hashes []common.Hash) bool {
	// The pending payload is also protected by the fork mutex, here we borrow it.
	w.forkMu.RLock()
	defer w.forkMu.RUnlock()

	for _, hash := range hashes {
		if !w.blobCache.Contains(hash) {
			return false
		}
	}
	return true
}

// getBlob tries to find blob data from the pending cache.
func (w *worker) getBlob(hash common.Hash) (hexutil.Bytes, hexutil.Bytes, []hexutil.Bytes) {
	// The pending payload is also protected by the fork mutex, here we borrow it.
	w.forkMu.RLock()
	defer w.forkMu.RUnlock()

	blob, commitment, proofs, ok := w.blobCache.Get(hash)
	if !ok {
		return nil, nil, nil
	}
	return blob, commitment, proofs
}

// cacheBlobs adds blob data to the pending cache, so mark as
// seen during this round of consensus. Only the blobs included
// in the hash list will be accepted.
func (w *worker) cacheBlobs(hashes []common.Hash, bundle *engine.BlobsBundle) {
	w.forkMu.Lock()
	defer w.forkMu.Unlock()

	w.blobCache.Add(hashes, bundle)
}

// feedback sends signed block back to EL for execution.
func (w *worker) feedback(block *types.Block, reorgCheck bool) error {
	// Lock to ensure EL RW atomicity, no concurrent reorg happens.
	w.forkMu.Lock()
	defer w.forkMu.Unlock()

	// Return if the block is already present in the chain.
	if w.chain.HasBlock(block.Hash(), block.NumberU64()) {
		return nil
	}

	// Transform from block to execution payload.
	payload := engine.BlockToExecutableData(block, nil, nil, nil)

	// Send payload through RPC. TODO: collect and include versioned hashes if possible
	status, err := w.sendPayload(payload.ExecutionPayload, make([]common.Hash, 0), &types.EmptyRootHash, payload.Requests, block.Time())
	if err != nil {
		return err
	}
	if status.Status == engine.INVALID {
		return fmt.Errorf("block rejected by EL, err: %v", *status.ValidationError)
	}

	// Set head based on reorg check. But if the block is not inserted to EL database,
	// we pass this check. It's possible that the EL needs a force signal to start.
	if reorgCheck && status.Status == engine.VALID {
		reorg, err := w.forker.ReorgNeeded(w.chain.CurrentHeader(), block.Header())
		if err != nil {
			return err
		}
		if !reorg {
			return nil
		}
	}

	// Send the latest head info to EL, and request a block proposal if mining.
	resp, err := w.sendForkChoice(block.Header(), uint64(time.Now().Unix()), w.isMining())
	if err != nil {
		return err
	}
	if resp.PayloadStatus.Status == engine.INVALID {
		return fmt.Errorf("set head failed, status: %v", resp.PayloadStatus.Status)
	}
	if resp.PayloadID != nil {
		w.pendingPayloadID = resp.PayloadID
	}
	return nil
}

// sendForkChoice sends new chain head information to EL miner API through RPC.
func (w *worker) sendForkChoice(head *types.Header, timestamp uint64, requestMine bool) (engine.ForkChoiceResponse, error) {
	update := engine.ForkchoiceStateV1{
		HeadBlockHash:      head.Hash(),
		SafeBlockHash:      head.ParentHash,
		FinalizedBlockHash: head.ParentHash,
	}
	slotNumber := uint64(0)
	targetGasLimit := uint64(60_000_000) // Use the default value, ref miner/miner.go DefaultConfig.GasCeil
	attributes := engine.PayloadAttributes{
		Timestamp:             timestamp,
		Random:                common.Hash{},
		SuggestedFeeRecipient: w.feeRecipient,
		Withdrawals:           make([]*types.Withdrawal, 0),
		BeaconRoot:            &types.EmptyRootHash,
		SlotNumber:            &slotNumber,
		TargetGasLimit:        &targetGasLimit,
	}

	var forkChoiceMethod string
	var resp engine.ForkChoiceResponse
	latest := w.chain.Config().LatestFork(timestamp)
	if latest >= forks.Amsterdam {
		// TODO: custodyColumns has not set
		forkChoiceMethod = "engine_forkchoiceUpdatedV4"
	} else if latest >= forks.Cancun {
		forkChoiceMethod = "engine_forkchoiceUpdatedV3"
		attributes.SlotNumber = nil
		attributes.TargetGasLimit = nil
	} else if latest >= forks.Shanghai {
		forkChoiceMethod = "engine_forkchoiceUpdatedV2"
		attributes.BeaconRoot = nil
		attributes.SlotNumber = nil
		attributes.TargetGasLimit = nil
	} else {
		forkChoiceMethod = "engine_forkchoiceUpdatedV2"
		attributes.Withdrawals = nil
		attributes.BeaconRoot = nil
		attributes.SlotNumber = nil
		attributes.TargetGasLimit = nil
	}

	// Set mining attributes only when the worker is set to be mining.
	var err error
	var params *engine.PayloadAttributes
	var custodyColumns *types.CustodyBitmap
	if requestMine {
		params = &attributes
	}
	switch forkChoiceMethod {
	case "engine_forkchoiceUpdatedV2", "engine_forkchoiceUpdatedV3":
		err = w.rpc.CallContext(w.ctx, &resp, forkChoiceMethod, update, params)
	case "engine_forkchoiceUpdatedV4", "engine_forkchoiceUpdatedV5":
		err = w.rpc.CallContext(w.ctx, &resp, forkChoiceMethod, update, params, custodyColumns)
	}
	if err != nil {
		return engine.ForkChoiceResponse{}, err
	}
	return resp, nil
}

// getPayload requests new block payload from EL miner through RPC.
func (w *worker) getPayload(payloadID *engine.PayloadID) (engine.ExecutionPayloadEnvelope, error) {
	var getPayloadMethod string
	var payload engine.ExecutionPayloadEnvelope
	switch payloadID.Version() {
	case engine.PayloadV1, engine.PayloadV2:
		getPayloadMethod = "engine_getPayloadV2"
	case engine.PayloadV3:
		getPayloadMethod = "engine_getPayloadV5"
	case engine.PayloadV4:
		getPayloadMethod = "engine_getPayloadV6"
	default:
		return engine.ExecutionPayloadEnvelope{}, fmt.Errorf("version %v is not supported for engine_getPayload", payloadID.Version())
	}
	err := w.rpc.CallContext(w.ctx, &payload, getPayloadMethod, payloadID)
	if err != nil {
		return engine.ExecutionPayloadEnvelope{}, err
	}
	return payload, nil
}

// sendPayload sends new block back to EL through RPC.
func (w *worker) sendPayload(payload *engine.ExecutableData, versionedHashes []common.Hash, beaconRoot *common.Hash, requests [][]byte, timestamp uint64) (engine.PayloadStatusV1, error) {
	var newPayloadMethod string
	var status engine.PayloadStatusV1
	latest := w.chain.Config().LatestFork(timestamp)
	if latest >= forks.Amsterdam {
		newPayloadMethod = "engine_newPayloadV5"
	} else if latest >= forks.Prague {
		newPayloadMethod = "engine_newPayloadV4"
	} else if latest >= forks.Cancun {
		newPayloadMethod = "engine_newPayloadV3"
	} else {
		newPayloadMethod = "engine_newPayloadV2"
	}
	switch newPayloadMethod {
	case "engine_newPayloadV2":
		err := w.rpc.CallContext(w.ctx, &status, newPayloadMethod, payload)
		if err != nil {
			return engine.PayloadStatusV1{}, err
		}
	case "engine_newPayloadV3":
		err := w.rpc.CallContext(w.ctx, &status, newPayloadMethod, payload, versionedHashes, beaconRoot)
		if err != nil {
			return engine.PayloadStatusV1{}, err
		}
	default:
		hexRequests := make([]hexutil.Bytes, len(requests))
		for i := range requests {
			hexRequests[i] = hexutil.Bytes(requests[i])
		}
		err := w.rpc.CallContext(w.ctx, &status, newPayloadMethod, payload, versionedHashes, beaconRoot, hexRequests)
		if err != nil {
			return engine.PayloadStatusV1{}, err
		}
	}
	return status, nil
}

// fetchBlobs fetches requested blobs from EL.
func (w *worker) fetchBlobs(hashes []common.Hash) (*engine.BlobsBundle, []common.Hash) {
	var bundle engine.BlobsBundle
	head := w.chain.CurrentHeader()
	missing := make([]common.Hash, 0, len(hashes))
	latest := w.chainConfig.LatestFork(head.Time)
	if latest >= forks.Osaka {
		var resp engine.BlobAndProofListV2
		err := w.rpc.CallContext(w.ctx, &resp, "engine_getBlobsV3", hashes)
		if err != nil {
			log.Error("Failed to fetch blobs", "err", err)
			return nil, hashes
		}
		for i, res := range resp {
			if res == nil {
				missing = append(missing, hashes[i])
				continue
			}
			bundle.Blobs = append(bundle.Blobs, res.Blob)
			bundle.Commitments = append(bundle.Commitments, res.Commitment)
			bundle.Proofs = append(bundle.Proofs, res.CellProofs...)
		}
	} else {
		var resp engine.BlobAndProofListV1
		err := w.rpc.CallContext(w.ctx, &resp, "engine_getBlobsV1", hashes)
		if err != nil {
			log.Error("Failed to fetch blobs", "err", err)
			return nil, hashes
		}
		for i, res := range resp {
			if res == nil {
				missing = append(missing, hashes[i])
				continue
			}
			// Convert to V1 blobs, since we've dropped the V0 support.
			var blob kzg4844.Blob
			copy(blob[:], res.Blob)
			cellProofs, err := kzg4844.ComputeCellProofs(&blob)
			if err != nil {
				log.Error("Failed to compute cell proofs", "err", err)
				missing = append(missing, hashes[i])
				continue
			}
			bundle.Blobs = append(bundle.Blobs, res.Blob)
			bundle.Commitments = append(bundle.Commitments, res.Commitment)
			for i := range cellProofs {
				bundle.Proofs = append(bundle.Proofs, cellProofs[i][:])
			}
		}
	}
	return &bundle, missing
}
