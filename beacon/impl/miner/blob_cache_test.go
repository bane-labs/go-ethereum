package miner

import (
	"testing"

	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
)

func testBlobBundle(ids ...byte) (*engine.BlobsBundle, []common.Hash) {
	bundle := &engine.BlobsBundle{}
	hashes := make([]common.Hash, 0, len(ids))
	for _, id := range ids {
		commitment := make(hexutil.Bytes, 48)
		commitment[0] = id
		bundle.Commitments = append(bundle.Commitments, commitment)
		bundle.Blobs = append(bundle.Blobs, hexutil.Bytes{id})
		for range kzg4844.CellProofsPerBlob {
			bundle.Proofs = append(bundle.Proofs, hexutil.Bytes{id})
		}
		hashes = append(hashes, convertKzgCommitmentToVersionedHash(commitment))
	}
	return bundle, hashes
}

func TestBlobCacheEvictsOldest(t *testing.T) {
	cache := newBlobCache(2)
	first, hashes := testBlobBundle(1, 2)
	cache.Add(hashes, first)

	third, thirdHashes := testBlobBundle(3)
	cache.Add(thirdHashes, third)

	if cache.Contains(hashes[0]) {
		t.Fatal("oldest blob is still cached")
	}
	if !cache.Contains(hashes[1]) || !cache.Contains(thirdHashes[0]) {
		t.Fatal("cache did not retain the newer blobs")
	}
	if len(cache.blobs) != 2 || len(cache.commitments) != 2 || len(cache.proofs) != 2 {
		t.Fatal("evicted blob data was not removed")
	}
}

func TestBlobCacheDuplicateDoesNotRefreshFIFO(t *testing.T) {
	cache := newBlobCache(2)
	first, hashes := testBlobBundle(1, 2)
	cache.Add(hashes, first)

	duplicate, duplicateHashes := testBlobBundle(1)
	cache.Add(duplicateHashes, duplicate)

	third, thirdHashes := testBlobBundle(3)
	cache.Add(thirdHashes, third)

	if cache.Contains(hashes[0]) || !cache.Contains(hashes[1]) || !cache.Contains(thirdHashes[0]) {
		t.Fatal("duplicate blob changed FIFO eviction order")
	}
}
