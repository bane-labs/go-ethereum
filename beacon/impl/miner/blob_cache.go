package miner

import (
	"crypto/sha256"
	"slices"

	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
)

// blobCache is a cache for blob sidecars and their hashes.
type blobCache struct {
	order []common.Hash // The order list of hashes in the cache, used for FIFO eviction.
	max   int

	hashes      map[common.Hash]struct{}
	blobs       map[common.Hash]hexutil.Bytes
	commitments map[common.Hash]hexutil.Bytes
	proofs      map[common.Hash][]hexutil.Bytes
}

// newBlobCache creates a new blobCache with the given maximum size.
func newBlobCache(max int) *blobCache {
	return &blobCache{
		hashes:      make(map[common.Hash]struct{}),
		blobs:       make(map[common.Hash]hexutil.Bytes),
		commitments: make(map[common.Hash]hexutil.Bytes),
		proofs:      make(map[common.Hash][]hexutil.Bytes),
		max:         max,
	}
}

// Add adds a blob bundle to the cache. If the cache is full, it will evict the oldest entry.
// Only blobs in the provided hash list will be added to the cache.
// If a blob is already in the cache, it will not be added again.
func (bc *blobCache) Add(hashes []common.Hash, bundle *engine.BlobsBundle) {
	if bc.max <= 0 {
		return
	}
	for i := range bundle.Blobs {
		hash := convertKzgCommitmentToVersionedHash(bundle.Commitments[i])
		if _, exists := bc.hashes[hash]; exists || !slices.Contains(hashes, hash) {
			// If the hash is already in the cache or not in the provided list, we skip adding it.
			// We don't want to store duplicate blobs or refresh their FIFO position.
			continue
		}
		if len(bc.order) >= bc.max {
			bc.evictOldest()
		}
		bc.hashes[hash] = struct{}{}
		bc.order = append(bc.order, hash)
		bc.blobs[hash] = bundle.Blobs[i]
		bc.commitments[hash] = bundle.Commitments[i]
		proofs := make([]hexutil.Bytes, kzg4844.CellProofsPerBlob)
		for n := 0; n < kzg4844.CellProofsPerBlob; n++ {
			proofs[n] = bundle.Proofs[i*kzg4844.CellProofsPerBlob+n]
		}
		bc.proofs[hash] = proofs
	}
}

// evictOldest removes the oldest entry from the cache.
func (bc *blobCache) evictOldest() {
	hash := bc.order[0]
	bc.order = bc.order[1:]
	delete(bc.hashes, hash)
	delete(bc.blobs, hash)
	delete(bc.commitments, hash)
	delete(bc.proofs, hash)
}

// Contains reports whether the given hash is in the cache.
func (bc *blobCache) Contains(hash common.Hash) bool {
	_, exists := bc.hashes[hash]
	return exists
}

// Get returns the blob, commitment, and proofs for the given hash, if it exists in the cache.
func (bc *blobCache) Get(hash common.Hash) (hexutil.Bytes, hexutil.Bytes, []hexutil.Bytes, bool) {
	if !bc.Contains(hash) {
		return nil, nil, nil, false
	}
	return bc.blobs[hash], bc.commitments[hash], bc.proofs[hash], true
}

const blobCommitmentVersionKZG uint8 = 0x01

func convertKzgCommitmentToVersionedHash(commitment []byte) common.Hash {
	versionedHash := sha256.Sum256(commitment)
	versionedHash[0] = blobCommitmentVersionKZG
	return versionedHash
}
