package dbft

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestCommit_Setters(t *testing.T) {
	var sign [extraSeal]byte
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Read(sign[:])

	var c commit
	c.SetSignature(sign[:])
	require.Equal(t, sign[:], c.Signature())
}

func TestCommit_RLP(t *testing.T) {
	var sign [extraSeal]byte
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
