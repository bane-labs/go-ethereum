package tpke

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAES(t *testing.T) {
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	g1, err := randPG1()
	require.NoError(t, err)
	t.Logf("origin msg : %v", string(msg))

	// Encrypt
	encrypted, err := AESEncrypt(g1, msg)
	require.NoError(t, err)
	t.Logf("encrypted msg : %v", encrypted)

	// Decrypt
	decrypted, err := AESDecrypt(g1, encrypted)
	require.NoError(t, err)
	t.Logf("decrypted msg : %v", string(decrypted))
}
