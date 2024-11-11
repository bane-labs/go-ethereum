package dbft

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func TestCommit_Setters(t *testing.T) {
	sign := make([]byte, crypto.SignatureLength)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Read(sign[:])

	var c = new(commit)
	c.SignatureExt = slices.Clone(sign)
	require.Equal(t, sign[:], c.Signature())
}

func TestCommit_RLP(t *testing.T) {
	sign := make([]byte, crypto.SignatureLength)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Read(sign[:])

	c := &commit{SignatureExt: sign}
	bytes, err := rlp.EncodeToBytes(c)
	require.NoError(t, err)

	decoded := &commit{}
	err = rlp.DecodeBytes(bytes, decoded)
	require.NoError(t, err)
	require.Equal(t, c, decoded)
}
