package dbft

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/stretchr/testify/require"
)

func TestPrepareRequest_RLP(t *testing.T) {
	pr := &prepareRequest{
		SealingProposal: &types.Header{
			ParentHash:       common.Hash{1, 2, 3},
			UncleHash:        common.Hash{1, 2, 3},
			Coinbase:         common.Address{1, 2, 3},
			Root:             common.Hash{1, 2, 3},
			TxHash:           common.Hash{1, 2, 3},
			ReceiptHash:      common.Hash{1, 2, 3},
			Bloom:            types.Bloom{1, 2, 3},
			Difficulty:       big.NewInt(0),
			Number:           big.NewInt(1),
			GasLimit:         0,
			GasUsed:          0,
			Time:             0,
			Extra:            []byte{1, 2, 3},
			MixDigest:        common.Hash{1, 2, 3},
			Nonce:            types.BlockNonce{},
			BaseFee:          nil,
			WithdrawalsHash:  nil,
			BlobGasUsed:      nil,
			ExcessBlobGas:    nil,
			ParentBeaconRoot: nil,
		},
		TxHashes:       []util.Uint256{util.Uint256{}},
		ParentSealHash: common.Hash{1, 2, 3},
		ParentExtra:    []byte{1, 2, 3},
	}

	bytes, err := rlp.EncodeToBytes(pr)
	require.NoError(t, err)

	decoded := &prepareRequest{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, pr, decoded)
}
