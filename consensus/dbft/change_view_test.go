package dbft

import (
	"testing"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestChangeView_Setters(t *testing.T) {
	var c changeView

	c.SetTimestamp(123) // in nanoseconds, no conversion expected.
	require.EqualValues(t, 123, c.Timestamp())

	c.SetNewViewNumber(2)
	require.EqualValues(t, 2, c.NewViewNumber())
}

func TestChangeView_RLP(t *testing.T) {
	c := &changeView{TimestampExt: 123, ReasonExt: 3}
	bytes, err := rlp.EncodeToBytes(c)
	require.NoError(t, err)

	decoded := &changeView{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, c, decoded)
}
