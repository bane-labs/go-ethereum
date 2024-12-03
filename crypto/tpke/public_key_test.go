package tpke

import (
	"strings"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/stretchr/testify/require"
)

func TestPublicKey_BytesFromBytes(t *testing.T) {
	t.Run("good", func(t *testing.T) {
		_, _, g1, _ := bls12381.Generators()
		expected := &PublicKey{
			pg1: &g1,
		}
		b := expected.Bytes()

		actual := new(PublicKey)
		require.NoError(t, actual.FromBytes(b))
		require.Equal(t, expected, actual)
	})

	t.Run("invalid length", func(t *testing.T) {
		actual := new(PublicKey)
		err := actual.FromBytes(make([]byte, PublicKeyLen+1))
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "invalid public key length: expected 48, got 49"), err)
	})
}
