package dbft

import (
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestPayloadSerializable(t *testing.T) {
	gk, _ := crypto.GenerateKey()
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
		TxHashes:         []common.Hash{},
		ParentSealHashV0: common.Hash{1, 2, 3},
		ParentExtra:      []byte{1, 2, 3},
	}
	expected := &Payload{
		Message: dbftproto.Message{
			ValidBlockStart: 0,
			ValidBlockEnd:   1,
			Sender:          crypto.PubkeyToAddress(gk.PublicKey),
			Data:            nil,
			Witness:         nil,
		},
		message: message{
			Type:           prepareRequestType,
			BlockIndex:     1,
			ValidatorIndex: 2,
			ViewNumber:     3,
			msgPayload:     pr,
		},
	}

	err := expected.Sign(&Signer{
		Signer: crypto.PubkeyToAddress(gk.PublicKey),
		SignFn: func(signer accounts.Account, mimeType string, message []byte) ([]byte, error) {
			return gk.Sign(rand.Reader, crypto.Keccak256(message), nil)
		},
	})
	require.NoError(t, err)
	require.NotNil(t, expected.Data)
	require.NotNil(t, expected.Witness)

	m := expected.Message
	h := m.Hash()
	require.NotNil(t, h)

	b, err := rlp.EncodeToBytes(m)
	require.NoError(t, err)

	var actual = new(dbftproto.Message)
	require.NoError(t, rlp.DecodeBytes(b, actual))
	require.Equal(t, expected.Message, *actual)

	actualP := payloadFromMessage(actual, nil)
	require.NoError(t, actualP.decodeData())

	require.Equal(t, expected, actualP)
}
