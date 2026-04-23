package dbft

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalUint256_SSZ(t *testing.T) {
	v := big.NewInt(113244324)
	expect := []byte{0xa4, 0xf8, 0xbf, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	require.Equal(t, marshalAsUint256(v), expect)
}
