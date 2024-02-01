package dbft

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// blockQueueCap is the number of tasks blockQueue can fit at once. It's OK for
// the blockQueue not to have a proper task for the newly-created block, and
// normally a single task is expected to be present in blockQueue. But we still
// need blockQueueCap restriction for the case of endless change views.
const blockQueueCap = 100

// blockQueue is an entity that collects sealed blocks from dBFT and routs these
// blocks to a proper place (either to miner or directly to chain).
type blockQueue struct {
	chain     ChainHeaderWriter
	tasksLock sync.RWMutex
	tasks     map[common.Hash]task
}

// task holds information about miner sealing task.
type task struct {
	height   uint64
	resCh    chan<- *types.Block
	cancelCh <-chan struct{}
}

// newBlockQueue creates an instance of blockQueue. It's not ready for usage until
// an instance of ChainHeaderWriter is properly set.
func newBlockQueue() *blockQueue {
	return &blockQueue{
		tasks: make(map[common.Hash]task),
	}
}

// SetChain initializes ChainHeaderWriter instanse needed for proper blockQueue
// functioning.
func (bq *blockQueue) SetChain(chain ChainHeaderWriter) {
	bq.chain = chain
}

// PutBlock routs block either to miner or (if there's no suitable sealing task)
// directly to blockchain. No block verification is performed, it is assumed that
// provided block is sealed and valid.
func (bq *blockQueue) PutBlock(b *types.Block, taskReceipts []*types.Receipt, taskState *state.StateDB) error {
	h := WorkerSealHash(b.Header())

	bq.tasksLock.Lock()
	task, ok := bq.tasks[h]

	bq.clearStaleTasks(b.NumberU64(), -1)

	if ok {
		var (
			err         error
			readByMiner bool
		)
		select {
		case <-task.cancelCh:
		case task.resCh <- b:
			readByMiner = true
		default:
			err = errors.New("sealing result is not read by miner, trying to insert block in chain manually")
		}
		delete(bq.tasks, h)

		if readByMiner {
			bq.tasksLock.Unlock()
			return nil
		}

		if err != nil {
			log.Warn(err.Error(),
				"number", b.Number(),
				"seal hash", h.String(),
				"hash", b.Hash().String(),
			)
		}
	}
	bq.tasksLock.Unlock()

	// If we're here then we're OK with that, it just means that:
	//  1) either dBFT received some extra commits and trying to
	//     send already constructed block one more time
	//  2) or worker has received block with the same index via network. Then
	//     we still need to save the block in case it has different hash.
	//  3) or we're not a primary node in this consensus round and thus,
	//     worker's task differs from the dBFT's proposal. In this case we
	//     need to try to insert block right into chain.
	if bq.chain.HasBlock(b.Hash(), b.NumberU64()) {
		return nil
	}

	if taskState == nil {
		_, err := bq.chain.InsertChain(types.Blocks{b})
		if err != nil {
			return fmt.Errorf("failed to insert block into chain: %w", err)
		}
		return nil
	}

	// Different block could share same sealhash, deep copy here to prevent write-write conflict.
	var (
		receipts = make([]*types.Receipt, len(taskReceipts))
		logs     []*types.Log
		hash     = b.Hash()
	)
	for i, taskReceipt := range taskReceipts {
		receipt := new(types.Receipt)
		receipts[i] = receipt
		*receipt = *taskReceipt

		// add block location fields
		receipt.BlockHash = hash
		receipt.BlockNumber = b.Number()
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
	_, err := bq.chain.WriteBlockAndSetHead(b, receipts, logs, taskState, true)
	if err != nil {
		log.Error("Failed writing block to chain and set head",
			"number", b.NumberU64(),
			"err", err)
		return fmt.Errorf("failed to write block into chain: %w", err)
	}
	log.Info("Successfully enqueued new block", "number", b.Number(), "hash", hash)

	return nil
}

// clearStaleTasks removes all stale tasks up to the specified height (including
// the height itself). It doesn't hold tasksLock, so it's the caller's responsibility.
func (bq *blockQueue) clearStaleTasks(till uint64, count int) {
	for h, task := range bq.tasks {
		if task.height <= till {
			delete(bq.tasks, h)
			count--
			if count <= 0 {
				break
			}
		}
	}
}

// SubmitTask adds subsequent miner task to the blockqueue instance.
func (bq *blockQueue) SubmitTask(sealHash common.Hash, number uint64, resCh chan<- *types.Block, cancelCh <-chan struct{}) {
	bq.tasksLock.Lock()
	defer bq.tasksLock.Unlock()

	// We're OK with the fact that capacity is reached, remove random outdated seal
	// task (it's likely won't be completed, and if it will, then the block will be
	// inserted to the chain directly).
	if len(bq.tasks) == blockQueueCap {
		bq.clearStaleTasks(number, 1)
	}

	// Do not check the existing task with the same hash. It could happen that new
	// sealing task has the same hash after ChangeView sealing proposal initialisation.
	bq.tasks[sealHash] = task{
		height:   number,
		resCh:    resCh,
		cancelCh: cancelCh,
	}
}
