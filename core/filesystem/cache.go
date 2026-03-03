package filesystem

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/core/types"
)

var ErrNotFound = errors.New("not found in storage")

// IsNotFound allows callers to treat errors from a flat-file database, where the file record is missing,
// as equivalent to ErrNotFound.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || os.IsNotExist(err)
}

// blobIndexMask is a bitmask representing the set of blob indices that are currently set.
type blobIndexMask []bool

// BlobStorageSummary represents cached information about the BlobSidecars on disk for each root the cache knows about.
type BlobStorageSummary struct {
	epoch            primitives.Epoch
	time             uint64
	mask             blobIndexMask
	maxBlobsPerBlock int
}

// HasIndex returns true if the BlobSidecar at the given index is available in the filesystem.
func (s BlobStorageSummary) HasIndex(idx uint64) bool {
	if idx >= uint64(len(s.mask)) {
		return false
	}
	return s.mask[idx]
}

// AllAvailable returns true if we have all blobs for all indices from 0 to count-1.
func (s BlobStorageSummary) AllAvailable(count int) bool {
	if count > len(s.mask) {
		return false
	}
	for i := range count {
		if !s.mask[i] {
			return false
		}
	}
	return true
}

func (s BlobStorageSummary) MaxBlobsForTime() uint64 {
	return uint64(s.maxBlobsPerBlock)
}

// NewBlobStorageSummary creates a new BlobStorageSummary for a given epoch and mask.
func NewBlobStorageSummary(epoch primitives.Epoch, time uint64, mask []bool, maxBlobsPerBlock int) (BlobStorageSummary, error) {
	if len(mask) != maxBlobsPerBlock {
		return BlobStorageSummary{}, fmt.Errorf("mask length %d does not match expected %d for epoch %d", len(mask), maxBlobsPerBlock, epoch)
	}
	return BlobStorageSummary{
		epoch:            epoch,
		time:             time,
		mask:             mask,
		maxBlobsPerBlock: maxBlobsPerBlock,
	}, nil
}

// BlobStorageSummarizer can be used to receive a summary of metadata about blobs on disk for a given root.
// The BlobStorageSummary can be used to check which indices (if any) are available for a given block by root.
type BlobStorageSummarizer interface {
	Summary(root [32]byte) BlobStorageSummary
}

type blobStorageSummaryCache struct {
	mu     sync.RWMutex
	nBlobs float64
	cache  map[[32]byte]BlobStorageSummary

	maxBlobsPerBlock func(time uint64) int
}

var _ BlobStorageSummarizer = &blobStorageSummaryCache{}

func newBlobStorageCache(maxBlobsPerBlock func(time uint64) int) *blobStorageSummaryCache {
	return &blobStorageSummaryCache{
		cache:            make(map[[32]byte]BlobStorageSummary),
		maxBlobsPerBlock: maxBlobsPerBlock,
	}
}

// Summary returns the BlobStorageSummary for `root`. The BlobStorageSummary can be used to check for the presence of
// BlobSidecars based on Index.
func (s *blobStorageSummaryCache) Summary(root [32]byte) BlobStorageSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[root]
}

func (s *blobStorageSummaryCache) ensure(ident blobIdent) error {
	maxBlobsPerBlock := s.maxBlobsPerBlock(ident.time)
	if ident.index >= uint64(maxBlobsPerBlock) {
		return errIndexOutOfBounds
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.cache[ident.root]
	v.epoch = ident.epoch
	v.time = ident.time
	v.maxBlobsPerBlock = maxBlobsPerBlock
	if v.mask == nil {
		v.mask = make(blobIndexMask, maxBlobsPerBlock)
	}
	if !v.mask[ident.index] {
		s.updateMetrics(1)
	}
	v.mask[ident.index] = true
	s.cache[ident.root] = v
	return nil
}

func (s *blobStorageSummaryCache) get(key [32]byte) (BlobStorageSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[key]
	return v, ok
}

func (s *blobStorageSummaryCache) identForIdx(key [32]byte, idx uint64) (blobIdent, error) {
	v, ok := s.get(key)
	if !ok || !v.HasIndex(idx) {
		return blobIdent{}, ErrNotFound
	}
	return blobIdent{
		root:  key,
		index: idx,
		epoch: v.epoch,
		time:  v.time,
	}, nil
}

func (s *blobStorageSummaryCache) identForRoot(key [32]byte) (blobIdent, error) {
	v, ok := s.get(key)
	if !ok {
		return blobIdent{}, ErrNotFound
	}
	return blobIdent{
		root:  key,
		epoch: v.epoch,
		time:  v.time,
	}, nil
}

func (s *blobStorageSummaryCache) evict(key [32]byte) int {
	deleted := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.cache[key]
	if !ok {
		return 0
	}
	for i := range v.mask {
		if v.mask[i] {
			deleted += 1
		}
	}
	delete(s.cache, key)
	if deleted > 0 {
		s.updateMetrics(-float64(deleted))
	}
	return deleted
}

func (s *blobStorageSummaryCache) updateMetrics(delta float64) {
	s.nBlobs += delta
	blobDiskCount.Set(s.nBlobs)
	blobDiskSize.Set(s.nBlobs * types.BlobSidecarSize)
}
