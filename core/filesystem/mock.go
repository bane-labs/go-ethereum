package filesystem

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/ethereum/go-ethereum/params"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

const testMaxBlobsPerBlock = 6

// Blobs
// -----

// NewEphemeralBlobStorage should only be used for tests.
// The instance of BlobStorage returned is backed by an in-memory virtual filesystem,
// improving test performance and simplifying cleanup.
func NewEphemeralBlobStorage(t testing.TB, opts ...BlobStorageOption) *BlobStorage {
	return NewWarmedEphemeralBlobStorageUsingFs(t, afero.NewMemMapFs(), opts...)
}

// NewEphemeralBlobStorageAndFs can be used by tests that want access to the virtual filesystem
// in order to interact with it outside the parameters of the BlobStorage api.
func NewEphemeralBlobStorageAndFs(t testing.TB, opts ...BlobStorageOption) (afero.Fs, *BlobStorage) {
	fs := afero.NewMemMapFs()
	bs := NewWarmedEphemeralBlobStorageUsingFs(t, fs, opts...)
	return fs, bs
}

func NewEphemeralBlobStorageUsingFs(t testing.TB, fs afero.Fs, opts ...BlobStorageOption) *BlobStorage {
	opts = append(opts,
		WithBlobRetentionEpochs(primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest)),
		WithFs(fs),
		WithChainConfig(GenerateMockBlobStorageChainConfig()))
	bs, err := NewBlobStorage(opts...)
	if err != nil {
		t.Fatalf("error initializing test BlobStorage, err=%s", err.Error())
	}
	return bs
}

func NewWarmedEphemeralBlobStorageUsingFs(t testing.TB, fs afero.Fs, opts ...BlobStorageOption) *BlobStorage {
	bs := NewEphemeralBlobStorageUsingFs(t, fs, opts...)
	bs.WarmCache()
	return bs
}

func NewMockBlobStorageSummarizer(t *testing.T, epoch primitives.Epoch, set map[[32]byte][]int) BlobStorageSummarizer {
	c := newBlobStorageCache(testMaxBlobsPerBlock)
	for k, v := range set {
		for i := range v {
			if err := c.ensure(blobIdent{root: k, epoch: epoch, index: uint64(v[i])}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return c
}

var (
	emptyBlob          = kzg4844.Blob{}
	emptyBlobCommit, _ = kzg4844.BlobToCommitment(&emptyBlob)
	emptyBlobProof, _  = kzg4844.ComputeBlobProof(&emptyBlob, emptyBlobCommit)
)

func GenerateMockBlobSidecar(t *testing.T, root [32]byte, blockNumber *big.Int, index int) types.ROBlob {
	pb := &types.BlobSidecar{
		Index:       uint64(index),
		BlockNumber: blockNumber,
		BlobTxSidecar: types.BlobTxSidecar{
			Blobs:       []kzg4844.Blob{emptyBlob},
			Commitments: []kzg4844.Commitment{emptyBlobCommit},
			Proofs:      []kzg4844.Proof{emptyBlobProof},
		},
	}
	r, err := types.NewROBlobWithRoot(pb, root)
	require.NoError(t, err)
	return r
}

func FakeVerifySliceForTest(t *testing.T, b []types.ROBlob) []types.VerifiedROBlob {
	// log so that t is truly required
	t.Log("producing fake []VerifiedROBlob for a test")
	vbs := make([]types.VerifiedROBlob, len(b))
	for i := range b {
		vbs[i] = types.NewVerifiedROBlob(b[i])
	}
	return vbs
}

func GenerateMockBlobStorageChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID: big.NewInt(10),
		BlobScheduleConfig: &params.BlobScheduleConfig{
			Cancun: params.DefaultCancunBlobConfig,
		},
	}
}
