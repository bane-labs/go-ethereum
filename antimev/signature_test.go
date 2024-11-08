package antimev

import (
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/common"
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
	dir := t.TempDir()
	// Init keystores
	addrs := make([]common.Address, size)
	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*KeyStore, size)
	for i := 0; i < size; i++ {
		addrs[i] = accounts[i].addr
		key, _ := crypto.HexToECDSA(accounts[i].msgPrivKey)
		pubs[i] = &ecies.ImportECDSA(key).PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(accounts[i].addr, ecies.ImportECDSA(key), size, threshold, accounts[i].pwd)
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		shareMsgs:   make([][][]byte, size),
		sharePVSSes: make([][]byte, size),
	}
	for i := 0; i < size; i++ {
		// No reshare to handle
		err := kss[i].OnValidatorList(addrs, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		msgs, pvss, err := kss[i].DKGShare()
		if err != nil {
			t.Fatalf(err.Error())
		}
		contract.shareMsgs[i] = msgs
		contract.sharePVSSes[i] = pvss
	}
	// Send secret sharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(kss[j].address, contract.shareMsgs[j], contract.sharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Aggregate pvss manually
	cmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		if err != nil {
			t.Fatalf(err.Error())
		}
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		if err != nil {
			t.Fatalf(err.Error())
		}
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange(encodePointG1(cmt))
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
