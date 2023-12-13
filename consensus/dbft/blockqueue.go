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
	tasksLock sync.RWMutex
	tasks     map[common.Hash]task // TODO: consider restricting queue capacity and make it a real queue.
}

// task holds information about miner sealing task.
type task struct {
	resCh    chan<- *types.Block
	cancalCh <-chan struct{}
}

// newBlockQueue creates an instance of blockQueue ready to be used.
func newBlockQueue() *blockQueue {
	return &blockQueue{
		tasks: make(map[common.Hash]task),
	}
}

// PutBlock routs block either to miner or (if there's no suitable sealing task)
// directly to blockchain. No block verification is performed, it is assumed that
// provided block is sealed and valid.
func (bq *blockQueue) PutBlock(b *types.Block) error {
	h := WorkerSealHash(b.Header())

	bq.tasksLock.Lock()
	defer bq.tasksLock.Unlock()

	task, ok := bq.tasks[h]
	if !ok {
		// We're OK with that, it just means that dBFT received some extra commits and trying to
		// send block one more time.
		return nil
	}

	// Seal interrupt is not possible with dBFT, thus, ignore stop channel.
	// TODO: check whether worker removes cancelled task from the list of sealing works before closing stop channel.
	var err error
	select {
	// TODO: ideally, we must replace this way of chain notification to some other (direct) way
	// and send block not to the miner but to the chain directly.
	case task.resCh <- b:
	default:
		err = fmt.Errorf("seaing result is not read by miner (sealhash %s)", h)
	}

	delete(bq.tasks, h)

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
