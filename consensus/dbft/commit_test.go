package dbft

import (
	"math/rand"
	"testing"
	"time"

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
