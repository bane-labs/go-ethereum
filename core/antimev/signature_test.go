package antimev

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

func TestSingleSignature(t *testing.T) {
	sk := tpke.RandomPrivateKey()
	pk := sk.GetPublicKey()

	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	share := sk.SignShare(msg)
	if !pk.VerifySigShare(msg, share) {
		t.Fatalf("invalid signature")
	}
}

func TestThresholdSignature(t *testing.T) {
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

	// Test functionality
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	shares := make(map[int]*tpke.SignatureShare)
	for i := 0; i < size; i++ {
		share, err := kss[i].SignShare(msg)
		if err != nil {
			t.Fatalf(err.Error())
		}
		shares[i+1] = share
	}

	sig, err := kss[0].AggregateAndVerifySig(msg, shares)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if sig == nil {
		t.Fatalf("invalid signature")
	}
}
