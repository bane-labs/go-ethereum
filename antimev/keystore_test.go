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

var (
	// accounts is a list of the seven-node privnet CN addresses/passwords sorted by
	// the order of CN nodes from CN1 to CN7. Do not modify this list in tests; make
	// a copy if modification is needed since some tests rely on the order of accounts.
	// Ref. https://github.com/bane-labs/go-ethereum/tree/bane-main/privnet/seven.
	accounts = []account{
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
		},
	}
	// accountsSorted is a list of seven-node privnet CN addresses/passwords sorted by
	// addresses. Do not modify this list in tests, make a copy if needed.
	accountsSorted []account
)

func init() {
	accountsSorted = slices.Clone(accounts)
	slices.SortFunc(accountsSorted, func(a, b account) int {
		return common.Address.Cmp(a.addr, b.addr)
	})
}

type MockContractStorage struct {
	reshareMsgs   [][][]byte
	resharePVSSes [][]byte
	shareMsgs     [][][]byte
	sharePVSSes   [][]byte
	recoverMsgs   [][][]byte
}

func TestDKG(t *testing.T) {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	dir := t.TempDir()
	// Init keystores
	cns := slices.Clone(accountsSorted)
	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*KeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(cns[i].addr, key, size, threshold, cns[i].pwd)
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
		_, _, err := kss[i].OnValidatorList(valList, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		// No reshare to handle
		msgs, pvss, err := kss[i].OnReshareFinish()
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
	// Only check sharing
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange()
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
	cns := slices.Clone(accountsSorted)
	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*KeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(cns[i].addr, key, size, threshold, cns[i].pwd)
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
		_, _, err := kss[i].OnValidatorList(valList, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		// No reshare to handle
		msgs, pvss, err := kss[i].OnReshareFinish()
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
		msgs, pvss, err := kss[i].OnValidatorList(valList, pubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		contract.reshareMsgs[i] = msgs
		contract.resharePVSSes[i] = pvss
	}
	// Send resharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretReshare(kss[j].address, contract.reshareMsgs[j], contract.resharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
		}
	}
	// Only check resharing
	for i := 0; i < size; i++ {
		_, _, err := kss[i].OnReshareFinish()
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
		reshareMsgs:   make([][][]byte, 0),
		resharePVSSes: make([][]byte, 0),
		shareMsgs:     make([][][]byte, 0),
		sharePVSSes:   make([][]byte, 0),
	}
	for i := 0; i < len(addrs); i++ {
		_, _, err := kss[i].OnValidatorList(addrs[:size], pubs[:size])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// No reshare to handle
		msgs, pvss, err := kss[i].OnReshareFinish()
		if err != nil {
			t.Fatalf(err.Error())
		}
		if len(msgs) > 0 {
			contract.sharePVSSes = append(contract.sharePVSSes, pvss)
			contract.shareMsgs = append(contract.shareMsgs, msgs)
		}
	}
	if len(contract.sharePVSSes) != size || len(contract.shareMsgs) != size {
		t.Fatalf("invalid message amount")
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
		msgs, pvss, err := kss[i].OnValidatorList(addrs[1:], pubs[1:])
		if err != nil {
			t.Fatalf(err.Error())
		}
		if len(msgs) > 0 {
			contract.resharePVSSes = append(contract.resharePVSSes, pvss)
			contract.reshareMsgs = append(contract.reshareMsgs, msgs)
		}
	}
	if len(contract.reshareMsgs) != size {
		t.Fatalf("invalid message amount")
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
		_, _, err := kss[i].OnReshareFinish()
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
		reshareMsgs:   make([][][]byte, 0),
		resharePVSSes: make([][]byte, 0),
		shareMsgs:     make([][][]byte, 0),
		sharePVSSes:   make([][]byte, 0),
		recoverMsgs:   make([][][]byte, 0),
	}
	for i := 0; i < len(addrs); i++ {
		_, _, err := kss[i].OnValidatorList(addrs[:size], pubs[:size])
		if err != nil {
			t.Fatalf(err.Error())
		}
		// No reshare to handle
		msgs, pvss, err := kss[i].OnReshareFinish()
		if err != nil {
			t.Fatalf(err.Error())
		}
		if len(msgs) > 0 {
			contract.sharePVSSes = append(contract.sharePVSSes, pvss)
			contract.shareMsgs = append(contract.shareMsgs, msgs)
		}
	}
	if len(contract.shareMsgs) != size {
		t.Fatalf("invalid message amount")
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
		msgs, pvss, err := kss[i].OnValidatorList(addrs[1:], pubs[1:])
		if err != nil {
			t.Fatalf(err.Error())
		}
		if len(msgs) > 0 {
			contract.reshareMsgs = append(contract.reshareMsgs, msgs)
			contract.resharePVSSes = append(contract.resharePVSSes, pvss)
		}
	}
	if len(contract.reshareMsgs) != size {
		t.Fatalf("invalid message amount")
	}
	// Send resharing messages, expect validator 7
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
		msgs, err := kss[i].OnRecoverStart(rIdxs, rAddrs, rPubs)
		if err != nil {
			t.Fatalf(err.Error())
		}
		// Length of msgs is 1
		if msgs != nil {
			contract.recoverMsgs = append(contract.recoverMsgs, msgs)
		}
	}
	if len(contract.recoverMsgs) != size {
		t.Fatalf("invalid message amount")
	}
	// Send recover messages, broadcast to all nodes
	for i := 0; i < size-1; i++ {
		err := kss[7].ReceiveRecoverShare(kss[i].address, contract.recoverMsgs[i][0])
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Recover the lost resharing messages
	msgs, pvss, err := kss[7].OnRecoverFinish()
	if err != nil {
		t.Fatalf(err.Error())
	}
	for i := 0; i < size; i++ {
		err := kss[i].ReceiveRecoveredReshare(kss[7].address, msgs, pvss)
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
	// Only check resharing
	for i := 0; i < size; i++ {
		_, _, err := kss[i].OnReshareFinish()
		if err != nil {
			t.Fatalf(err.Error())
		}
	}
}
