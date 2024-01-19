package dbft

import (
	"testing"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestRecoveryRequest_RLP(t *testing.T) {
	c := &recoveryRequest{TimestampExt: 12345}
	bytes, err := rlp.EncodeToBytes(c)
	require.NoError(t, err)

	decoded := &recoveryRequest{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, c, decoded)
}
