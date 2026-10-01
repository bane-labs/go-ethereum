package beacon

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// Constants to match up protocol versions and messages
const (
	BEACON2 = 2
	BEACON3 = 3
)

// ProtocolName is the official short name of the `beacon` protocol used during
// devp2p capability negotiation.
const ProtocolName = "beacon"

// ProtocolVersions are the supported versions of the `beacon` protocol (first
// is primary).
var ProtocolVersions = []uint{BEACON2, BEACON3}

// protocolLengths are the number of implemented message corresponding to
// different protocol versions.
var protocolLengths = map[uint]uint64{BEACON2: 10, BEACON3: 12}

// maxMessageSize is the maximum cap on the size of a protocol message.
const maxMessageSize = 10 * 1024 * 1024

const (
	StatusMsg                = 0x00 // Status message
	NewBlockHashesMsg        = 0x01 // New block hashes message
	NewBlockMsg              = 0x02 // New block message
	NewBlobsRootMsg          = 0x03 // New blobs root message
	GetBlobsMsg              = 0x04 // Get blobs message
	BlobsMsg                 = 0x05 // Blobs message
	GetBatchBlobsMsg         = 0x06 // Get batch blobs message
	BatchBlobsMsg            = 0x07 // Batch blobs message
	GetCachedTransactionsMsg = 0x08 // Get cached transactions message
	CachedTransactionsMsg    = 0x09 // Cached transactions message
	GetCachedBlobsMsg        = 0x0A // Get cached blobs message
	CachedBlobsMsg           = 0x0B // Cached blobs message
)

var (
	errNoStatusMsg             = errors.New("no status message")
	errMsgTooLarge             = errors.New("message too long")
	errDecode                  = errors.New("invalid message")
	errInvalidMsgCode          = errors.New("invalid message code")
	errProtocolVersionMismatch = errors.New("protocol version mismatch")
	errNetworkIDMismatch       = errors.New("network ID mismatch")
	errGenesisMismatch         = errors.New("genesis mismatch")
	errForkIDRejected          = errors.New("fork ID rejected")
)

// Packet represents a p2p message in the `beacon` protocol.
type Packet interface {
	Name() string // Name returns a string corresponding to the message type.
	Kind() byte   // Kind returns the message type.
}

// StatusPacket is the network packet for the status message.
type StatusPacket struct {
	ProtocolVersion uint32
	NetworkID       uint64
	TD              *big.Int
	Head            common.Hash
	HeadNumber      uint64
	Genesis         common.Hash
	ForkID          forkid.ID
	BlobSync        bool
}

// NewBlockHashesPacket is the network packet for the block announcements.
type NewBlockHashesPacket []struct {
	Hash   common.Hash // Hash of one particular block being announced
	Number uint64      // Number of one particular block being announced
}

// Unpack retrieves the block hashes and numbers from the announcement packet
// and returns them in a split flat format that's more consistent with the
// internal data structures.
func (p *NewBlockHashesPacket) Unpack() ([]common.Hash, []uint64) {
	var (
		hashes  = make([]common.Hash, len(*p))
		numbers = make([]uint64, len(*p))
	)
	for i, body := range *p {
		hashes[i], numbers[i] = body.Hash, body.Number
	}
	return hashes, numbers
}

// NewBlockPacket is the network packet for the block propagation message.
type NewBlockPacket struct {
	Block *types.Block
	TD    *big.Int
}

// NewBlobsRootPacket is the network packet for blobs block hash.
type NewBlobsRootPacket struct {
	BlockHash common.Hash
}

// GetBlobsPacket is the request packet for blob query by block hash.
type GetBlobsPacket struct {
	RequestId uint64
	BlockHash common.Hash
	Ttl       uint8
}

// BlobsPacket1 is the response packet for blobs by block hash, with BEACON1.
type BlobsPacket1 struct {
	RequestId uint64
	Sidecars  types.BlobSidecarV0s
}

// BlobsPacket is the response packet for blobs by block hash.
type BlobsPacket struct {
	RequestId uint64
	Sidecars  types.BlobSidecars
}

// BlobsRLP is used for replying to blobs by block hash requests, in cases
// where we already have them RLP-encoded, and thus can avoid the decode-encode
// roundtrip.
type BlobsRLPPacket struct {
	RequestId        uint64
	BlobsRLPResponse rlp.RawValue
}

// GetBatchBlobsRequest represents a blobs query.
type GetBatchBlobsRequest []common.Hash

// GetBatchBlobsPacket is the request packet for batch blob query by block hashes.
type GetBatchBlobsPacket struct {
	RequestId uint64
	GetBatchBlobsRequest
}

// BatchBlobsResponse is the response packet for blobs by block hash.
type BatchBlobsResponse [][]*types.BlobTxSidecar

// BatchBlobsPacket is the response packet for batch blobs by block hashes.
type BatchBlobsPacket struct {
	RequestId uint64
	BatchBlobsResponse
}

// BatchBlobsRLPResponse is used for replying to blobs by block hash requests, in cases
// where we already have them RLP-encoded, and thus can avoid the decode-encode
// roundtrip.
type BatchBlobsRLPResponse []rlp.RawValue

// BatchBlobsRLPPacket is the BatchBlobsRLPResponse with request ID wrapping.
type BatchBlobsRLPPacket struct {
	RequestId uint64
	BatchBlobsRLPResponse
}

// GetCachedTransactionsRequest represents a cached transaction query.
type GetCachedTransactionsRequest []common.Hash

// GetCachedTransactionsPacket represents a cached transaction query with request ID wrapping.
type GetCachedTransactionsPacket struct {
	RequestId uint64
	GetCachedTransactionsRequest
}

// CachedTransactionsResponse is the network packet for cached transaction distribution.
type CachedTransactionsResponse []*types.Transaction

// CachedTransactionsPacket is the network packet for cached transaction distribution
// with request ID wrapping.
type CachedTransactionsPacket struct {
	RequestId uint64
	CachedTransactionsResponse
}

// CachedTransactionsRLPResponse is the network packet for cached transaction distribution, used
// in the cases we already have them in rlp-encoded form
type CachedTransactionsRLPResponse []rlp.RawValue

// CachedTransactionsRLPPacket is CachedTransactionsRLPResponse with request ID wrapping.
type CachedTransactionsRLPPacket struct {
	RequestId uint64
	CachedTransactionsRLPResponse
}

// GetCachedBlobsRequest represents a cached blob query.
type GetCachedBlobsRequest []common.Hash

// GetCachedBlobsPacket represents a cached blob query with request ID wrapping.
type GetCachedBlobsPacket struct {
	RequestId uint64
	GetCachedBlobsRequest
}

// CachedBlobsResponse is the network packet for cached blob distribution.
type CachedBlobsResponse struct {
	Commitments []hexutil.Bytes
	Blobs       []hexutil.Bytes
	Proofs      []hexutil.Bytes
}

// CachedBlobsPacket is the network packet for cached blob distribution
// with request ID wrapping.
type CachedBlobsPacket struct {
	RequestId uint64
	CachedBlobsResponse
}

func (*StatusPacket) Name() string { return "Status" }
func (*StatusPacket) Kind() byte   { return StatusMsg }

func (*NewBlockHashesPacket) Name() string { return "NewBlockHashes" }
func (*NewBlockHashesPacket) Kind() byte   { return NewBlockHashesMsg }

func (*NewBlockPacket) Name() string { return "NewBlock" }
func (*NewBlockPacket) Kind() byte   { return NewBlockMsg }

func (*NewBlobsRootPacket) Name() string { return "NewBlobsRoot" }
func (*NewBlobsRootPacket) Kind() byte   { return NewBlobsRootMsg }

func (*GetBlobsPacket) Name() string { return "GetBlobs" }
func (*GetBlobsPacket) Kind() byte   { return GetBlobsMsg }

func (*BlobsPacket) Name() string { return "Blobs" }
func (*BlobsPacket) Kind() byte   { return BlobsMsg }

func (*GetBatchBlobsPacket) Name() string { return "GetBatchBlobs" }
func (*GetBatchBlobsPacket) Kind() byte   { return GetBatchBlobsMsg }

func (*BatchBlobsPacket) Name() string { return "BatchBlobs" }
func (*BatchBlobsPacket) Kind() byte   { return BatchBlobsMsg }

func (*GetCachedTransactionsRequest) Name() string { return "GetCachedTransactions" }
func (*GetCachedTransactionsRequest) Kind() byte   { return GetCachedTransactionsMsg }

func (*CachedTransactionsResponse) Name() string { return "CachedTransactions" }
func (*CachedTransactionsResponse) Kind() byte   { return CachedTransactionsMsg }

func (*GetCachedBlobsRequest) Name() string { return "GetCachedBlobs" }
func (*GetCachedBlobsRequest) Kind() byte   { return GetCachedBlobsMsg }

func (*CachedBlobsResponse) Name() string { return "CachedBlobs" }
func (*CachedBlobsResponse) Kind() byte   { return CachedBlobsMsg }
