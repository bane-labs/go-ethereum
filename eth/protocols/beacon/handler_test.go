package beacon

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/holiman/uint256"
)

var (
	emptyBlob          = kzg4844.Blob{}
	emptyBlobCommit, _ = kzg4844.BlobToCommitment(&emptyBlob)
	emptyBlobProof, _  = kzg4844.ComputeBlobProof(&emptyBlob, emptyBlobCommit)
)

// testBackend implements the Backend interface for testing
type testBackend struct {
}

func (b *testBackend) RunPeer(peer *Peer, handler Handler) error {
	// Normally the backend would do peer maintenance and handshakes. All that
	// is omitted and we will just give control back to the handler.
	return handler(peer)
}
func (b *testBackend) PeerInfo(enode.ID) interface{} { panic("not implemented") }

func (b *testBackend) Handle(peer *Peer, packet Packet) error {
	return nil
}

// Tests that blobs can be retrieved from a remote peer based on user queries.
func TestGetBlobs1(t *testing.T) { testGetBlobs(t, BEACON1) }

func testGetBlobs(t *testing.T, protocol uint) {
	t.Parallel()

	backend := &testBackend{}

	peer, _ := newTestPeer("peer", protocol, backend)
	defer peer.close()

	// Test cases
	tests := []struct {
		name string
		msg  *GetBlobsPacket
	}{
		{
			name: "Valid request with block hash",
			msg: &GetBlobsPacket{
				RequestId: 1,
				BlockHash: common.HexToHash("0x123"),
			},
		},
	}

	// Run each of the tests and verify the results against the chain
	for i, tt := range tests {
		blobs := make([]*types.BlobTxSidecar, 0)
		blob := &types.BlobTxSidecar{
			Blobs:       []kzg4844.Blob{emptyBlob},
			Commitments: []kzg4844.Commitment{emptyBlobCommit},
			Proofs:      []kzg4844.Proof{emptyBlobProof},
		}
		blobs = append(blobs, blob)
		// Send the hash request and verify the response
		p2p.Send(peer.app, GetBlobsMsg, tt.msg)
		if err := p2p.ExpectMsg(peer.app, BlobsByRootMsg, &BlobsByRootPacket{
			RequestId: tt.msg.RequestId,
			Sidecars:  blobs,
		}); err != nil {
			t.Errorf("test %d: blobs mismatch: %v", i, err)
		}
	}
}

// mockMsg implements the Decoder interface for testing
type mockMsg struct {
	code uint64
	data interface{}
}

func (m *mockMsg) Decode(val interface{}) error {
	// Simple implementation for testing
	switch v := val.(type) {
	case *GetBlobsPacket:
		*v = *m.data.(*GetBlobsPacket)
	case *BlobsByRootPacket:
		*v = *m.data.(*BlobsByRootPacket)
	}
	return nil
}

func (m *mockMsg) Time() time.Time {
	return time.Now()
}

func TestHandleBlobsByRoot(t *testing.T) {
	t.Parallel()

	backend := &testBackend{}

	peer, _ := newTestPeer("peer", BEACON1, backend)
	defer peer.close()

	// Create test blocks
	blockBlobs := make([]*types.BlobSidecars, 3)
	for i := 0; i < 3; i++ {
		body := &types.Body{
			Transactions: []*types.Transaction{
				createMockBlobTx(createMockSidecar()),
			},
		}
		blockBlobs[i] = collectBlobsFromTxs(body.Transactions)
	}

	// Test cases
	tests := []struct {
		name    string
		msg     *mockMsg
		wantErr bool
	}{
		{
			name: "Valid blob response",
			msg: &mockMsg{
				code: BlobsByRootMsg,
				data: &BlobsByRootPacket{
					RequestId: 1,
					Sidecars:  *blockBlobs[0],
				},
			},
			wantErr: false,
		},
		{
			name: "Empty blob response",
			msg: &mockMsg{
				code: BlobsByRootMsg,
				data: &BlobsByRootPacket{
					RequestId: 2,
					Sidecars:  types.BlobSidecars{},
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid request ID",
			msg: &mockMsg{
				code: BlobsByRootMsg,
				data: &BlobsByRootPacket{
					RequestId: 0,
					Sidecars:  *blockBlobs[0],
				},
			},
			wantErr: false,
		},
		{
			name: "Non-continuous blocks",
			msg: &mockMsg{
				code: BlobsByRootMsg,
				data: &BlobsByRootPacket{
					RequestId: 3,
					Sidecars:  *blockBlobs[0],
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleBlobsByRoot(backend, tt.msg, peer.Peer)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleBlocksByRange() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func createMockBlobTx(sidecar *types.BlobTxSidecar) *types.Transaction {
	if sidecar == nil {
		tx := &types.DynamicFeeTx{
			ChainID:   big.NewInt(0),
			Nonce:     0,
			GasTipCap: big.NewInt(22),
			GasFeeCap: big.NewInt(5),
			Gas:       25000,
			To:        &common.Address{0x03, 0x04, 0x05},
			Value:     big.NewInt(99),
			Data:      make([]byte, 50),
		}
		return types.NewTx(tx)
	}
	tx := &types.BlobTx{
		ChainID:    uint256.NewInt(0),
		Nonce:      5,
		GasTipCap:  uint256.NewInt(22),
		GasFeeCap:  uint256.NewInt(5),
		Gas:        25000,
		To:         common.Address{0x03, 0x04, 0x05},
		Value:      uint256.NewInt(99),
		Data:       make([]byte, 50),
		BlobFeeCap: uint256.NewInt(15),
		BlobHashes: sidecar.BlobHashes(),
		Sidecar:    sidecar,
	}
	return types.NewTx(tx)
}

func createMockSidecar() *types.BlobTxSidecar {
	return &types.BlobTxSidecar{
		Blobs:       []kzg4844.Blob{emptyBlob, emptyBlob, emptyBlob, emptyBlob},
		Commitments: []kzg4844.Commitment{emptyBlobCommit, emptyBlobCommit, emptyBlobCommit, emptyBlobCommit},
		Proofs:      []kzg4844.Proof{emptyBlobProof, emptyBlobProof, emptyBlobProof, emptyBlobProof},
	}
}

func collectBlobsFromTxs(txs types.Transactions) *types.BlobSidecars {
	sidecars := make(types.BlobSidecars, 0, len(txs))
	for _, tx := range txs {
		sidecar := tx.BlobTxSidecar()
		if sidecar == nil {
			continue
		}
		sidecars = append(sidecars, sidecar)
	}
	return &sidecars
}
