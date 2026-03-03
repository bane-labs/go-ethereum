package filesystem

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path"
	"sort"
	"testing"

	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestRootFromDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		err  error
		root [32]byte
	}{
		{
			name: "happy path",
			dir:  "0xffff875e1d985c5ccb214894983f2428edb271f0f87b68ba7010e4a99df3b5cb",
			root: [32]byte{255, 255, 135, 94, 29, 152, 92, 92, 203, 33, 72, 148, 152, 63, 36, 40,
				237, 178, 113, 240, 248, 123, 104, 186, 112, 16, 228, 169, 157, 243, 181, 203},
		},
		{
			name: "too short",
			dir:  "0xffff875e1d985c5ccb214894983f2428edb271f0f87b68ba7010e4a99df3b5c",
			err:  errInvalidRootString,
		},
		{
			name: "too log",
			dir:  "0xffff875e1d985c5ccb214894983f2428edb271f0f87b68ba7010e4a99df3b5cbb",
			err:  errInvalidRootString,
		},
		{
			name: "missing prefix",
			dir:  "ffff875e1d985c5ccb214894983f2428edb271f0f87b68ba7010e4a99df3b5cb",
			err:  errInvalidRootString,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, err := stringToRoot(c.dir)
			if c.err != nil {
				require.ErrorIs(t, err, c.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.root, root)
		})
	}
}

type dirFiles struct {
	name     string
	isDir    bool
	children []dirFiles
}

func (df dirFiles) reify(t *testing.T, fs afero.Fs, base string) {
	fullPath := path.Join(base, df.name)
	if df.isDir {
		if df.name != "" {
			require.NoError(t, fs.Mkdir(fullPath, directoryPermissions()))
		}
		for _, c := range df.children {
			c.reify(t, fs, fullPath)
		}
	} else {
		fp, err := fs.Create(fullPath)
		require.NoError(t, err)
		_, err = fp.WriteString("derp")
		require.NoError(t, err)
	}
}

func (df dirFiles) childNames() []string {
	cn := make([]string, len(df.children))
	for i := range df.children {
		cn[i] = df.children[i].name
	}
	return cn
}

func TestListDir(t *testing.T) {
	fs := afero.NewMemMapFs()
	rootStrs := []string{
		"0x0023dc5d063c7c1b37016bb54963c6ff4bfe5dfdf6dac29e7ceeb2b8fa81ed7a",
		"0xff30526cd634a5af3a09cc9bff67f33a621fc5b975750bb4432f74df077554b4",
		"0x23f5f795aaeb78c01fadaf3d06da2e99bd4b3622ae4dfea61b05b7d9adb119c2",
	}

	// parent directory
	tree := dirFiles{isDir: true}
	// break out each subdir for easier assertions
	notABlob := dirFiles{name: "notABlob", isDir: true}
	childlessBlob := dirFiles{name: rootStrs[0], isDir: true}
	blobWithSsz := dirFiles{name: rootStrs[1], isDir: true,
		children: []dirFiles{{name: "1.ssz"}, {name: "2.ssz"}},
	}
	blobWithSszAndTmp := dirFiles{name: rootStrs[2], isDir: true,
		children: []dirFiles{{name: "5.ssz"}, {name: "0.part"}}}
	tree.children = append(tree.children,
		notABlob, childlessBlob, blobWithSsz, blobWithSszAndTmp)

	topChildren := make([]string, len(tree.children))
	for i := range tree.children {
		topChildren[i] = tree.children[i].name
	}

	var filter = func(entries []string, filt func(string) bool) []string {
		filtered := make([]string, 0, len(entries))
		for i := range entries {
			if filt(entries[i]) {
				filtered = append(filtered, entries[i])
			}
		}
		return filtered
	}

	tree.reify(t, fs, "")
	cases := []struct {
		name     string
		dirPath  string
		expected []string
		filter   func(string) bool
		err      error
	}{
		{
			name:     "non-existent",
			dirPath:  "derp",
			expected: []string{},
			err:      os.ErrNotExist,
		},
		{
			name:     "empty",
			dirPath:  childlessBlob.name,
			expected: []string{},
		},
		{
			name:     "top",
			dirPath:  ".",
			expected: topChildren,
		},
		{
			name:     "custom filter: only notABlob",
			dirPath:  ".",
			expected: []string{notABlob.name},
			filter: func(s string) bool {
				return s == notABlob.name
			},
		},
		{
			name:     "root filter",
			dirPath:  ".",
			expected: []string{childlessBlob.name, blobWithSsz.name, blobWithSszAndTmp.name},
			filter:   IsBlockRootDir,
		},
		{
			name:     "ssz filter",
			dirPath:  blobWithSsz.name,
			expected: blobWithSsz.childNames(),
			filter:   isSszFile,
		},
		{
			name:     "ssz mixed filter",
			dirPath:  blobWithSszAndTmp.name,
			expected: []string{"5.ssz"},
			filter:   isSszFile,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := listDir(fs, c.dirPath)
			if c.filter != nil {
				result = filter(result, c.filter)
			}
			if c.err != nil {
				require.ErrorIs(t, err, c.err)
				require.Equal(t, 0, len(result))
			} else {
				require.NoError(t, err)
				sort.Strings(c.expected)
				sort.Strings(result)
				require.Equal(t, c.expected, result)
			}
		})
	}
}

type iterationTestTarget struct {
	ident             blobIdent
	blockNumberOffset uint64
	path              string
}

func ezIdent(t *testing.T, rootStr string, epoch primitives.Epoch, time uint64, index uint64) blobIdent {
	r, err := stringToRoot(rootStr)
	require.NoError(t, err)
	return blobIdent{root: r, epoch: epoch, time: time, index: index}
}

func setupTestBlobFile(t *testing.T, ident blobIdent, blockNumberOffset uint64, fs afero.Fs, l fsLayout) {
	sidecar := GenerateMockBlobSidecar(t, [32]byte{}, big.NewInt(0).SetUint64(uint64(ident.epoch)*params.BlocksPerEthEpoch+blockNumberOffset), int(ident.index))

	scb, err := rlp.EncodeToBytes(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	require.NoError(t, err)
	dir := l.dir(ident)
	require.NoError(t, fs.MkdirAll(dir, directoryPermissions()))
	p := l.sszPath(ident)
	require.NoError(t, afero.WriteFile(fs, p, scb, 0666))
	_, err = fs.Stat(p)
	require.NoError(t, err)
}

func TestIterationComplete(t *testing.T) {
	de := primitives.Epoch(1000)
	targets := []iterationTestTarget{
		{
			ident: ezIdent(t, "0x0125e54c64c925018c9296965a5b622d9f5ab626c10917860dcfb6aa09a0a00b", de+1234, 1766741589, 0),
			path:  "by-epoch/%d/%d/0x0125e54c64c925018c9296965a5b622d9f5ab626c10917860dcfb6aa09a0a00b-1766741589/0.ssz",
		},
		{
			ident:             ezIdent(t, "0x0127dba6fd30fdbb47e73e861d5c6e602b38ac3ddc945bb6a2fc4e10761e9a86", de+5330, 1766741589, 0),
			blockNumberOffset: 31,
			path:              "by-epoch/%d/%d/0x0127dba6fd30fdbb47e73e861d5c6e602b38ac3ddc945bb6a2fc4e10761e9a86-1766741589/0.ssz",
		},
		{
			ident:             ezIdent(t, "0x0127dba6fd30fdbb47e73e861d5c6e602b38ac3ddc945bb6a2fc4e10761e9a86", de+5330, 1766741589, 1),
			blockNumberOffset: 31,
			path:              "by-epoch/%d/%d/0x0127dba6fd30fdbb47e73e861d5c6e602b38ac3ddc945bb6a2fc4e10761e9a86-1766741589/1.ssz",
		},
		{
			ident:             ezIdent(t, "0x0232521756a0b965eab2c2245d7ad85feaeaf5f427cd14d1a7531f9d555b415c", -1+math.MaxUint64/32, 1766741589, 0),
			blockNumberOffset: 16,
			path:              "by-epoch/%d/%d/0x0232521756a0b965eab2c2245d7ad85feaeaf5f427cd14d1a7531f9d555b415c-1766741589/0.ssz",
		},
		{
			ident:             ezIdent(t, "0x0232521756a0b965eab2c2245d7ad85feaeaf5f427cd14d1a7531f9d555b415c", -1+math.MaxUint64/32, 1766741589, 1),
			blockNumberOffset: 16,
			path:              "by-epoch/%d/%d/0x0232521756a0b965eab2c2245d7ad85feaeaf5f427cd14d1a7531f9d555b415c-1766741589/1.ssz",
		},
		{
			ident:             ezIdent(t, "0x42eabe3d2c125410cd226de6f2825fb7575ab896c3f52e43de1fa29e4c809aba", -1+math.MaxUint64/32, 1766741589, 0),
			blockNumberOffset: 16,
			path:              "by-epoch/%d/%d/0x42eabe3d2c125410cd226de6f2825fb7575ab896c3f52e43de1fa29e4c809aba-1766741589/0.ssz",
		},
		{
			ident: ezIdent(t, "0x666cea5034e22bd3b849cb33914cad59afd88ee08e4d5bc0e997411c945fbc1d", de+11235, 1766741589, 1),
			path:  "by-epoch/%d/%d/0x666cea5034e22bd3b849cb33914cad59afd88ee08e4d5bc0e997411c945fbc1d-1766741589/1.ssz",
		},
	}
	fs := afero.NewMemMapFs()
	cache := newBlobStorageCache(func(time uint64) int { return testMaxBlobsPerBlock })
	byEpoch, err := newLayout(LayoutNameByEpoch, fs, cache, nil, primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest))
	require.NoError(t, err)
	for _, tar := range targets {
		setupTestBlobFile(t, tar.ident, tar.blockNumberOffset, fs, byEpoch)
	}
	iter, err := byEpoch.iterateIdents(0)
	require.NoError(t, err)
	nIdents := 0
	for ident, err := iter.next(); err != io.EOF; ident, err = iter.next() {
		require.NoError(t, err)
		nIdents++
		require.NoError(t, cache.ensure(ident))
	}
	require.Equal(t, len(targets), nIdents)
	for _, tar := range targets {
		entry, ok := cache.get(tar.ident.root)
		require.Equal(t, true, ok)
		require.Equal(t, tar.ident.epoch, entry.epoch)
		require.Equal(t, true, entry.HasIndex(tar.ident.index))
		path := fmt.Sprintf(tar.path, periodForEpoch(tar.ident.epoch, primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest)), tar.ident.epoch)
		require.Equal(t, path, byEpoch.sszPath(tar.ident))
	}
}
