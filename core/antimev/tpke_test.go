package antimev

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

func TestTPKE(t *testing.T) {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)

	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*AMEVKeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks, err := NewKeyStore(addrs[i], key, size, threshold)
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}

	msgbox := make([][][]byte, size)
	pvssbox := make([][]byte, size)
	for i := 0; i < size; i++ {
		// No reshare to handle
		_, _, err := kss[i].OnValidatorList(addrs, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Handle share
		msgs, pvss, err := kss[i].OnReshareFinish()
		if err != nil {
			t.Fatalf(err.Error())
		}
		msgbox[i] = msgs
		pvssbox[i] = pvss
	}

	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(kss[j].address, msgbox[j], pvssbox[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}

	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}

	// Encrypt
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	encryptedKey, encryptedMsg, err := kss[0].Encrypt(msg)
	if err != nil {
		t.Fatalf(err.Error())
	}

	// Verify ciphertext
	if err := encryptedKey.Verify(); err != nil {
		t.Fatalf("invalid ciphertext.")
	}

	// Generate shares
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < size; i++ {
		share, err := kss[i].DecryptWithShare([]*tpke.CipherText{encryptedKey})
		if err != nil {
			t.Fatalf(err.Error())
		}
		shares[i+1] = share
	}

	// Decrypt
	results, err := kss[0].AggregateAndDecryptWithShare([]*tpke.CipherText{encryptedKey}, [][]byte{encryptedMsg}, shares)
	if err != nil {
		t.Fatalf(err.Error())
	}
	for i := 0; i < len(msg); i++ {
		if msg[i] != results[0][i] {
			t.Fatalf("decryption failed.")
		}
	}
}
