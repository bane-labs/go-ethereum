package dbft

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestPrepareResponse_Setters(t *testing.T) {
	var p = prepareResponse{
		PreparationHashExt: common.Hash{1, 2, 3},
	}

	require.Equal(t, common.Hash{1, 2, 3}, p.PreparationHash())
}

func TestPrepareResponse_RLP(t *testing.T) {
	c := &prepareResponse{PreparationHashExt: common.Hash{1}}
	bytes, err := rlp.EncodeToBytes(c)
	require.NoError(t, err)

	decoded := &prepareResponse{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, c, decoded)
}
