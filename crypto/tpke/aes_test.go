package tpke

import (
	"testing"
)

func TestAES(t *testing.T) {
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	g1 := randPG1()
	t.Logf("origin msg : %v", string(msg))

	// Encrypt
	encrypted, err := AESEncrypt(g1, msg)
	if err != nil {
		t.Fatalf("encryption failed.")
	}
	t.Logf("encrypted msg : %v", encrypted)

	// Decrypt
	decrypted, err := AESDecrypt(g1, encrypted)
	if err != nil {
		t.Fatalf("decryption failed.")
	}
	t.Logf("decrypted msg : %v", string(decrypted))
}
