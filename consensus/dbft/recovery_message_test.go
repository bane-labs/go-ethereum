package dbft

import (
	"math/big"
	"math/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestRecoverMessage_RLP(t *testing.T) {
	sign := make([]byte, crypto.SignatureLength)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Read(sign[:])

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
		TxHashes:         []common.Hash{common.Hash{}},
		ParentSealHashV0: common.Hash{1, 2, 3},
		ParentExtra:      []byte{1, 2, 3},
	}

	rm := &recoveryMessage{
		PreparationHashExt: &common.Hash{1, 2},
		PreparationPayloads: []*preparationCompact{
			{ValidatorIndex: 1, InvocationScript: []byte{1, 2, 3}},
		},
		PreCommitPayloads: []*preCommitCompact{{
			ViewNumber:       1,
			ValidatorIndex:   2,
			Data:             []byte{1, 2, 3},
			InvocationScript: []byte{3, 4, 5},
		}},
		CommitPayloads:     []*commitCompact{{ViewNumber: 1, ValidatorIndex: 1, Signature: sign, InvocationScript: []byte{1, 2, 3, 4, 5}}},
		ChangeViewPayloads: []*changeViewCompact{{ValidatorIndex: 1, OriginalViewNumber: 2, Timestamp: 3, InvocationScript: []byte{1, 2, 3, 4, 5, 6}}},
		PrepareRequest: &message{
			Type:           prepareRequestType,
			BlockIndex:     1,
			ValidatorIndex: 2,
			ViewNumber:     3,
			msgPayload:     pr,
		},
	}

	bytes, err := rlp.EncodeToBytes(rm)
	require.NoError(t, err)

	decoded := &recoveryMessage{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, rm, decoded)
}
