package dbft

import (
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// blockQueue is an entity that collects sealed blocks from dBFT and routs these
// blocks to a proper place (either to miner or directly to chain).
type blockQueue struct {
	chain     ChainHeaderWriter
	tasksLock sync.RWMutex
	tasks     map[common.Hash]task // TODO: consider restricting queue capacity and make it a real queue.
}

// task holds information about miner sealing task.
type task struct {
	resCh    chan<- *types.Block
	cancalCh <-chan struct{}
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
func (bq *blockQueue) PutBlock(b *types.Block) error {
	h := WorkerSealHash(b.Header())

	bq.tasksLock.Lock()

	task, ok := bq.tasks[h]
	if !ok {
		bq.tasksLock.Unlock()
		// We're OK with that, it just means that:
		//  1) either dBFT received some extra commits and trying to
		//     send already constructed block one more time
		//  2) or we're not a primary node in this consensus round and thus,
		//     worker's task differs from the dBFT's proposal. In this case we
		//     need to try to insert block right into chain.
		if bq.chain.HasBlock(b.Hash(), b.NumberU64()) {
			return nil
		}

		// TODO: it's a very invasive way, we must be VERY careful about it, we MUST
		// review all the consequences of such insertion, because standard syncing
		// mechanism currently doesn't allow P2P blocks sync for the current consensus
		// nodes, see the code around:
		// log.Warn("Snap syncing, discarded propagated block", "number", blocks[0].Number(), "hash", blocks[0].Hash())
		//
		// and event if snap sync is off, then read the comment starting with:
		// // The blocks from the p2p network is regarded as untrusted
		//
		// So we may use either `h.chain.InsertBlockWithoutSetHead(block)` or bq.chain.InsertChain(types.Blocks{b}):
		// err := bq.chain.InsertBlockWithoutSetHead(b)
		_, err := bq.chain.InsertChain(types.Blocks{b})
		if err != nil {
			return fmt.Errorf("failed to insert block into chain: %w", err)
		}

		return nil
	}
	delete(bq.tasks, h)
	bq.tasksLock.Unlock()

	// Seal interrupt is not possible with dBFT, thus, ignore stop channel.
	// TODO: check whether worker removes cancelled task from the list of sealing works before closing stop channel.
	var err error
	select {
	case task.resCh <- b:
	case <-task.cancalCh:
	default:
		err = fmt.Errorf("seaing result is not read by miner (sealhash %s)", h)
	}

	return err
}

// SubmitTask adds subsequent miner task to the blockqueue instance.
func (bq *blockQueue) SubmitTask(sealHash common.Hash, resCh chan<- *types.Block, cancelCh <-chan struct{}) error {
	bq.tasksLock.Lock()
	defer bq.tasksLock.Unlock()

	if _, ok := bq.tasks[sealHash]; ok {
		// Likely a program bug (incorrect Seal proposal verification), should never happen.
		return fmt.Errorf("duplicating sealing task is not allowed for dBFT: %s", sealHash)
	}

	bq.tasks[sealHash] = task{
		resCh:    resCh,
		cancalCh: cancelCh,
	}
	return nil
}
