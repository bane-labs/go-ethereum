package antimev

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
)

var size = 7
var threshold = 5

// account is a structure combining CN node address and its password in the privnet setup.
type account struct {
	addr common.Address
	pwd  string
}

// Here use the same address list as the seven-node privnet.
// Ref https://github.com/bane-labs/go-ethereum/tree/bane-main/privnet/seven.
var accounts = []account{
	{
		common.HexToAddress("0x74f4effb0b538baec703346b03b6d9292f53a4cd"),
		"fBfgE23FfqSVZRCGzFZbFvqabF3Ewvcg",
	}, {
		common.HexToAddress("0x910ad1641b7125eff746accdca1f11148b22f472"),
		"2fGwcFf14fVVZTRDqcFqCtSA4FDTXqXz",
	}, {
		common.HexToAddress("0xfef5f250af14df73f983caab7b1f5002189c42e0"),
		"RWDCWc3DqvRaf3vbqtzdRqQXfVqFcDw5",
	}, {
		common.HexToAddress("0xc51964013acbc6b271feecb0febd9e7a01202930"),
		"2xDvRCASaqCQs5e4cD2fAcScCaBxX3Zv",
	}, {
		common.HexToAddress("0xc5bbd9652546bc96be3dec97a38ee335f7873dfa"),
		"r3Sc25F54rzDdgC5VtBCzWcZwsAvEa5g",
	}, {
		common.HexToAddress("0x26f1794b81df2b832545b8b6bbca196b82e4feb1"),
		"4vaT1GgAVbDGZeVarCC2AVR55rxarcsa",
	}, {
		common.HexToAddress("0x0b51369d02e47ee3f143391b837aa08c31aaa19b"),
		"VxwXgET3VF1d453rvCazQVDAwBraCqsq",
	}, {
		common.HexToAddress("0x1f013ef87a88b3a77a405efba90c20ab0c2cb91a"),
		"gvZCas2wF3gScsGV3we1acAaG2dEqq5d",
	},
}

type MockContractStorage struct {
	reshareMsgs   [][][]byte
	resharePVSSes [][]byte
	shareMsgs     [][][]byte
	sharePVSSes   [][]byte
	recoverMsgs   [][][]byte
}

func TestShare(t *testing.T) {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	dir := t.TempDir()
	// Init keystores
	cns := accounts[:size]
	slices.SortFunc(cns, func(a, b account) int {
		return common.Address.Cmp(a.addr, b.addr)
	})
	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*KeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(accounts[i].addr, key, size, threshold, accounts[i].pwd)
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
	valList := make([]common.Address, size)
	for i := range cns {
		valList[i] = cns[i].addr
	}
	for i := 0; i < size; i++ {
		// No reshare to handle
		msgs, pvss, _, _, err := kss[i].OnValidatorList(valList, pubs)
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
	for i := 0; i < size; i++ {
		// Only check sharing
		err := kss[i].aggregateShare()
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Try finish DKG without resharing
		err = kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
}

func TestReshare(t *testing.T) {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	dir := t.TempDir()
	// Init keystores
	cns := accounts[:size]
	slices.SortFunc(cns, func(a, b account) int {
		return common.Address.Cmp(a.addr, b.addr)
	})
	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*KeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(accounts[i].addr, key, size, threshold, accounts[i].pwd)
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		reshareMsgs:   make([][][]byte, size),
		resharePVSSes: make([][]byte, size),
		shareMsgs:     make([][][]byte, size),
		sharePVSSes:   make([][]byte, size),
	}
	valList := make([]common.Address, size)
	for i := range cns {
		valList[i] = cns[i].addr
	}
	for i := 0; i < size; i++ {
		// No resharing to handle
		msgs, pvss, _, _, err := kss[i].OnValidatorList(valList, pubs)
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
	// Finalize dkg
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Execute resharing this time
	for i := 0; i < size; i++ {
		sMsgs, sPvss, rMsgs, rPvss, err := kss[i].OnValidatorList(valList, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		contract.shareMsgs[i] = sMsgs
		contract.sharePVSSes[i] = sPvss
		contract.reshareMsgs[i] = rMsgs
		contract.resharePVSSes[i] = rPvss
	}
	// Send resharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretReshare(kss[j].address, contract.reshareMsgs[j], contract.resharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
			err = kss[i].ReceiveSecretShare(kss[j].address, contract.shareMsgs[j], contract.sharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Check sharing and resharing
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
}

func TestGroupChange(t *testing.T) {
	// Add another address for group change
	addrs := []common.Address{
		common.HexToAddress("0xcbbeca26e89011e32ba25610520b20741b809007"),
		common.HexToAddress("0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc"),
		common.HexToAddress("0xd10f47396dc6c76ad53546158751582d3e2683ef"),
		common.HexToAddress("0xa51fe05b0183d01607bf48c1718d1168a1c11171"),
		common.HexToAddress("0x01b517b301bb143476da35bb4a1399500d925514"),
		common.HexToAddress("0x7976ad987d572377d39fb4bab86c80e08b6f8327"),
		common.HexToAddress("0xd711da2d8c71a801fc351163337656f1321343a0"),
		common.HexToAddress("0xd94b88c9d92845256019ee3bd9b07a57ca067970"),
	}
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	dir := t.TempDir()
	// Init keystores
	pubs := make([]*ecies.PublicKey, len(addrs))
	kss := make([]*KeyStore, len(addrs))
	for i := 0; i < len(addrs); i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(addrs[i], key, size, threshold, "pwd")
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		reshareMsgs:   make([][][]byte, size),
		resharePVSSes: make([][]byte, size),
		shareMsgs:     make([][][]byte, size),
		sharePVSSes:   make([][]byte, size),
	}
	for i := 0; i < len(addrs); i++ {
		// No resharing to handle
		msgs, pvss, _, _, err := kss[i].OnValidatorList(addrs[:size], pubs[:size])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Sharing members
		if i < size {
			contract.shareMsgs[i] = msgs
			contract.sharePVSSes[i] = pvss
		}
		// Not a member
		if i == size {
			if len(msgs) > 0 || len(pvss) > 0 {
				t.Fatalf("Only member should share secrets")
			}
		}
	}
	// Send secret sharing messages, broadcast to all nodes
	for i := 0; i < len(addrs); i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(kss[j].address, contract.shareMsgs[j], contract.sharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Finalize dkg
	for i := 0; i < len(addrs); i++ {
		err := kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Execute resharing this time
	for i := 0; i < len(addrs); i++ {
		_, _, rMsgs, rPvss, err := kss[i].OnValidatorList(addrs[1:], pubs[1:])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Resharing members
		if i < size {
			contract.reshareMsgs[i] = rMsgs
			contract.resharePVSSes[i] = rPvss
		}
		// Not a member
		if i == size {
			if len(rMsgs) > 0 || len(rPvss) > 0 {
				t.Fatalf("Only member should reshare secrets")
			}
		}
	}
	// Send resharing messages, broadcast to all nodes
	for i := 0; i < len(addrs); i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretReshare(kss[j].address, contract.reshareMsgs[j], contract.resharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Only check resharing
	for i := 0; i < len(addrs); i++ {
		err := kss[i].aggregateReshare()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
}

func TestRecover(t *testing.T) {
	// Add another address for group change
	addrs := []common.Address{
		common.HexToAddress("0xcbbeca26e89011e32ba25610520b20741b809007"),
		common.HexToAddress("0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc"),
		common.HexToAddress("0xd10f47396dc6c76ad53546158751582d3e2683ef"),
		common.HexToAddress("0xa51fe05b0183d01607bf48c1718d1168a1c11171"),
		common.HexToAddress("0x01b517b301bb143476da35bb4a1399500d925514"),
		common.HexToAddress("0x7976ad987d572377d39fb4bab86c80e08b6f8327"),
		common.HexToAddress("0xd711da2d8c71a801fc351163337656f1321343a0"),
		common.HexToAddress("0xd94b88c9d92845256019ee3bd9b07a57ca067970"),
	}
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	dir := t.TempDir()
	// Init keystores
	pubs := make([]*ecies.PublicKey, len(addrs))
	kss := make([]*KeyStore, len(addrs))
	for i := 0; i < len(addrs); i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(addrs[i], key, size, threshold, "pwd")
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		reshareMsgs:   make([][][]byte, size),
		resharePVSSes: make([][]byte, size),
		shareMsgs:     make([][][]byte, size),
		sharePVSSes:   make([][]byte, size),
		recoverMsgs:   make([][][]byte, size),
	}
	for i := 0; i < len(addrs); i++ {
		// No resharing to handle
		msgs, pvss, _, _, err := kss[i].OnValidatorList(addrs[:size], pubs[:size])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Sharing members
		if i < size {
			contract.shareMsgs[i] = msgs
			contract.sharePVSSes[i] = pvss
		}
		// Not a member
		if i == size {
			if len(msgs) > 0 || len(pvss) > 0 {
				t.Fatalf("Only member should share secrets")
			}
		}
	}
	// Send secret sharing messages, broadcast to all nodes
	for i := 0; i < len(addrs); i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(kss[j].address, contract.shareMsgs[j], contract.sharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Finalize dkg
	for i := 0; i < len(addrs); i++ {
		err := kss[i].OnEpochChange()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Execute resharing this time
	for i := 0; i < len(addrs); i++ {
		_, _, rMsgs, rPvss, err := kss[i].OnValidatorList(addrs[1:], pubs[1:])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Resharing members
		if i < size {
			contract.reshareMsgs[i] = rMsgs
			contract.resharePVSSes[i] = rPvss
		}
		// Not a member
		if i == size {
			if len(rMsgs) > 0 || len(rPvss) > 0 {
				t.Fatalf("Only member should reshare secrets")
			}
		}
	}
	// Send resharing messages, expect which from validator 7
	for i := 0; i < len(addrs); i++ {
		for j := 0; j < size-1; j++ {
			err := kss[i].ReceiveSecretReshare(kss[j].address, contract.reshareMsgs[j], contract.resharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Execute recovering this time, dead index 7, recover to validator 8
	rIdxs := []int{7}
	rAddrs := []common.Address{addrs[7]}
	rPubs := []*ecies.PublicKey{pubs[7]}
	for i := 0; i < len(addrs); i++ {
		msgs, err := kss[i].StartRecover(rIdxs, rAddrs, rPubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Length of msgs is 1
		if msgs != nil {
			contract.recoverMsgs[i] = msgs
		}
	}
	// Send recover messages, broadcast to all nodes
	for i := 0; i < size-1; i++ {
		err := kss[7].ReceiveRecoverShare(kss[i].address, contract.recoverMsgs[i][0])
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Recover the lost resharing messages
	msgs, pvss, err := kss[7].TryRecoverReshare()
	if err != nil {
		t.Fatalf(err.Error())
	}
	for i := 0; i < len(addrs); i++ {
		err := kss[i].ReceiveRecoveredReshare(kss[7].address, msgs, pvss)
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Only check resharing
	for i := 0; i < size; i++ {
		err := kss[i].aggregateReshare()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
}
