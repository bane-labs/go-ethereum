package filesystem

import (
	"encoding/binary"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

type prunerScenario struct {
	name            string
	prunedBefore    primitives.Epoch
	retentionPeriod primitives.Epoch
	latest          primitives.Epoch
	expected        pruneExpectation
}

type pruneExpectation struct {
	called  bool
	arg     primitives.Epoch
	summary *pruneSummary
	err     error
}

func (e *pruneExpectation) record(before primitives.Epoch) (*pruneSummary, error) {
	e.called = true
	e.arg = before
	if e.summary == nil {
		e.summary = &pruneSummary{}
	}
	return e.summary, e.err
}

func TestPrunerNotify(t *testing.T) {
	defaultRetention := primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest)
	cases := []prunerScenario{
		{
			name:            "last epoch of period",
			retentionPeriod: defaultRetention,
			prunedBefore:    11235,
			latest:          defaultRetention + 11235,
			expected:        pruneExpectation{called: false},
		},
		{
			name:            "within period",
			retentionPeriod: defaultRetention,
			prunedBefore:    11235,
			latest:          11235 + defaultRetention - 1,
			expected:        pruneExpectation{called: false},
		},
		{
			name:            "triggers",
			retentionPeriod: defaultRetention,
			prunedBefore:    11235,
			latest:          11235 + 1 + defaultRetention,
			expected:        pruneExpectation{called: true, arg: 11235 + 1},
		},
		{
			name:            "from zero - before first period",
			retentionPeriod: defaultRetention,
			prunedBefore:    0,
			latest:          defaultRetention - 1,
			expected:        pruneExpectation{called: false},
		},
		{
			name:            "from zero - at boundary",
			retentionPeriod: defaultRetention,
			prunedBefore:    0,
			latest:          defaultRetention,
			expected:        pruneExpectation{called: false},
		},
		{
			name:            "from zero - triggers",
			retentionPeriod: defaultRetention,
			prunedBefore:    0,
			latest:          defaultRetention + 1,
			expected:        pruneExpectation{called: true, arg: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actual := &pruneExpectation{}
			l := &mockLayout{pruneBeforeFunc: actual.record}
			pruner := &blobPruner{retentionPeriod: c.retentionPeriod}
			pruner.prunedBefore.Store(uint64(c.prunedBefore))
			done := pruner.notify(c.latest, l)
			<-done
			require.Equal(t, c.expected.called, actual.called)
			require.Equal(t, c.expected.arg, actual.arg)
		})
	}
}

func testSetupBlobIdentPaths(t *testing.T, fs afero.Fs, bs *BlobStorage, idents []testIdent) []blobIdent {
	created := make([]blobIdent, len(idents))
	for i, id := range idents {
		sidecar := GenerateMockBlobSidecar(t, id.root, big.NewInt(0).SetUint64(uint64(id.epoch)*params.BlocksPerEthEpoch+id.blockNumberOffset), int(id.index))
		scs := FakeVerifySliceForTest(t, []types.ROBlob{sidecar})
		require.NoError(t, bs.Save(scs[0]))
		ident := identForSidecar(scs[0])
		_, err := fs.Stat(bs.layout.sszPath(ident))
		require.NoError(t, err)
		created[i] = ident
	}
	return created
}

func testAssertBlobsPruned(t *testing.T, fs afero.Fs, bs *BlobStorage, pruned, remain []blobIdent) {
	for _, id := range pruned {
		_, err := fs.Stat(bs.layout.sszPath(id))
		require.NotNil(t, err)
		require.Equal(t, true, os.IsNotExist(err))
	}
	for _, id := range remain {
		_, err := fs.Stat(bs.layout.sszPath(id))
		require.NoError(t, err)
	}
}

type testIdent struct {
	blobIdent
	blockNumberOffset uint64
}

func testRoots(n int) [][32]byte {
	roots := make([][32]byte, n)
	for i := range roots {
		binary.LittleEndian.PutUint32(roots[i][:], uint32(1+i))
	}
	return roots
}

func TestLayoutPruneBefore(t *testing.T) {
	electra := primitives.Epoch(1)
	roots := testRoots(10)
	cases := []struct {
		name        string
		pruned      []testIdent
		remain      []testIdent
		pruneBefore primitives.Epoch
		err         error
		sum         pruneSummary
	}{
		{
			name:        "none pruned",
			pruneBefore: electra + 1,
			pruned:      []testIdent{},
			remain: []testIdent{
				{blockNumberOffset: 1, blobIdent: blobIdent{root: roots[0], epoch: electra + 1, index: 0}},
				{blockNumberOffset: 1, blobIdent: blobIdent{root: roots[1], epoch: electra + 1, index: 0}},
			},
		},
		{
			name:        "expected pruned before epoch",
			pruneBefore: electra + 3,
			pruned: []testIdent{
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[0], epoch: electra + 1, index: 0}},
				{blockNumberOffset: 31, blobIdent: blobIdent{root: roots[1], epoch: electra + 1, index: 5}},
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[2], epoch: electra + 2, index: 0}},
				{blockNumberOffset: 31, blobIdent: blobIdent{root: roots[3], epoch: electra + 2, index: 3}},
			},
			remain: []testIdent{
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[4], epoch: electra + 3, index: 2}},  // boundary
				{blockNumberOffset: 31, blobIdent: blobIdent{root: roots[5], epoch: electra + 3, index: 0}}, // boundary
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[6], epoch: electra + 4, index: 1}},
				{blockNumberOffset: 31, blobIdent: blobIdent{root: roots[7], epoch: electra + 4, index: 5}},
			},
			sum: pruneSummary{blobsPruned: 4},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs, bs := NewEphemeralBlobStorageAndFs(t, WithLayout(LayoutNameByEpoch))
			pruned := testSetupBlobIdentPaths(t, fs, bs, c.pruned)
			remain := testSetupBlobIdentPaths(t, fs, bs, c.remain)
			sum, err := bs.layout.pruneBefore(c.pruneBefore)
			if c.err != nil {
				require.ErrorIs(t, err, c.err)
				return
			}
			require.NoError(t, err)
			testAssertBlobsPruned(t, fs, bs, pruned, remain)
			require.Equal(t, c.sum.blobsPruned, sum.blobsPruned)
			require.Equal(t, len(c.pruned), sum.blobsPruned)
			require.Equal(t, len(c.sum.failedRemovals), len(sum.failedRemovals))
		})
	}
}

func TestLayoutByEpochPruneBefore(t *testing.T) {
	roots := testRoots(10)
	cases := []struct {
		name   string
		pruned []testIdent
		remain []testIdent
		err    error
		sum    pruneSummary
	}{
		{
			name: "single epoch period cleanup",
			pruned: []testIdent{
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[0], epoch: 307076, index: 0}},
			},
			remain: []testIdent{
				{blockNumberOffset: 0, blobIdent: blobIdent{root: roots[1], epoch: 371176, index: 0}}, // Different period
			},
			sum: pruneSummary{blobsPruned: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs, bs := NewEphemeralBlobStorageAndFs(t, WithLayout(LayoutNameByEpoch))
			pruned := testSetupBlobIdentPaths(t, fs, bs, c.pruned)
			remain := testSetupBlobIdentPaths(t, fs, bs, c.remain)

			time.Sleep(1 * time.Second)
			for _, id := range pruned {
				_, err := fs.Stat(bs.layout.sszPath(id))
				require.Equal(t, true, os.IsNotExist(err))

				dirs := bs.layout.blockParentDirs(id)
				for i := len(dirs) - 1; i > 0; i-- {
					_, err = fs.Stat(dirs[i])
					require.Equal(t, true, os.IsNotExist(err))
				}
			}
			for _, id := range remain {
				_, err := fs.Stat(bs.layout.sszPath(id))
				require.NoError(t, err)
			}
		})
	}
}
