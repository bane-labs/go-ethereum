package antimev

import (
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/bane-labs/zk-dkg/encryption"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/stretchr/testify/require"
)

// 7-CN privnet related constants.
const (
	size      = 7
	threshold = 5
	magic     = 2312251829
)

// account is a structure combining CN node address and its password in the privnet setup.
type account struct {
	addr       common.Address
	pwd        string
	msgPrivKey string
}

// accounts is a list of the seven-node privnet CN addresses/passwords sorted by
// the order of CN nodes from CN1 to CN7. Do not modify this list in tests; make
// a copy if modification is needed since some tests rely on the order of accounts.
// Ref. https://github.com/bane-labs/go-ethereum/tree/bane-main/privnet/seven.
var accounts = []account{
	{
		common.HexToAddress("0x5e6D9680428e6fe62a09BBb6AC23Df5bFE069AE8"),
		"",
		"400fe4a0c2d74a4fb5cab917cc0e344f6ad33916b8e0e2e3815d2943134ddbe7",
	},
	{
		common.HexToAddress("0x74f4effb0b538baec703346b03b6d9292f53a4cd"),
		"fBfgE23FfqSVZRCGzFZbFvqabF3Ewvcg",
		"0d0244862b62f2f4d6c2202b296e8cc84acd2921407c4a4e2b8ad341ddf12a8b",
	}, {
		common.HexToAddress("0x910ad1641b7125eff746accdca1f11148b22f472"),
		"2fGwcFf14fVVZTRDqcFqCtSA4FDTXqXz",
		"da124526518be708571dfc2af60bc16cce56ba679b867069eace340a5caf8ddd",
	}, {
		common.HexToAddress("0xfef5f250af14df73f983caab7b1f5002189c42e0"),
		"RWDCWc3DqvRaf3vbqtzdRqQXfVqFcDw5",
		"3689920e1b1eb9709539aa7d1504b8593e7d6e0ccc9b6947bdc8d824b3eeeba3",
	}, {
		common.HexToAddress("0xc51964013acbc6b271feecb0febd9e7a01202930"),
		"2xDvRCASaqCQs5e4cD2fAcScCaBxX3Zv",
		"4cec022d6349c8236db7dac14c854d8c60ed6574ae2705e6b22830afeb480dc5",
	}, {
		common.HexToAddress("0xc5bbd9652546bc96be3dec97a38ee335f7873dfa"),
		"r3Sc25F54rzDdgC5VtBCzWcZwsAvEa5g",
		"934ec9b8674064112fc39d5123c7f16907e901c07c1f7216ede96f0369bfcb4a",
	}, {
		common.HexToAddress("0x26f1794b81df2b832545b8b6bbca196b82e4feb1"),
		"4vaT1GgAVbDGZeVarCC2AVR55rxarcsa",
		"f853f01aba25bc1374d79e37cc41e3e914102695bed90ee50eea3c0ed557e52d",
	}, {
		common.HexToAddress("0x0b51369d02e47ee3f143391b837aa08c31aaa19b"),
		"VxwXgET3VF1d453rvCazQVDAwBraCqsq",
		"63e8d515b467d512dfaf3b25634a6d68cd666955dbd7f260cb3a817f67ae51ac",
	}, {
		common.HexToAddress("0x1f013ef87a88b3a77a405efba90c20ab0c2cb91a"),
		"gvZCas2wF3gScsGV3we1acAaG2dEqq5d",
		"71abf6553a0edf00d2beba93d7c711a88067a84b7d387ccb349e185f4d9ce9c4",
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
		require.NoError(t, err)
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		shareMsgs:   make([][][]byte, size),
		sharePVSSes: make([][]byte, size),
	}
	for i := 0; i < size; i++ {
		// No reshare to handle
		kss[i].OnSharePeriodStart(false)
		ss, pvss, err := kss[i].DKGShare()
		require.NoError(t, err)
		contract.shareMsgs[i], err = encryptShareMessages(pubs, ss)
		require.NoError(t, err)
		contract.sharePVSSes[i] = pvss
	}
	// Send secret sharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	cmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	for i := 0; i < size; i++ {
		// Try finish DKG without resharing
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
		require.NoError(t, err)
	}
}

func TestReshare(t *testing.T) {
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
		require.NoError(t, err)
		kss[i] = ks
	}
	// Ignore resharing and execute sharing
	contract := &MockContractStorage{
		reshareMsgs:   make([][][]byte, size),
		resharePVSSes: make([][]byte, size),
		shareMsgs:     make([][][]byte, size),
		sharePVSSes:   make([][]byte, size),
	}
	for i := 0; i < size; i++ {
		// No reshare to handle
		kss[i].OnSharePeriodStart(false)
		ss, pvss, err := kss[i].DKGShare()
		require.NoError(t, err)
		contract.shareMsgs[i], err = encryptShareMessages(pubs, ss)
		require.NoError(t, err)
		contract.sharePVSSes[i] = pvss
	}
	// Send secret sharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	cmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	// Finalize dkg
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
		require.NoError(t, err)
	}
	// Execute resharing this time
	for i := 0; i < size; i++ {
		kss[i].OnSharePeriodStart(false)
		rss, rPvss, err := kss[i].DKGReshare()
		require.NoError(t, err)
		ss, sPvss, err := kss[i].DKGShare()
		require.NoError(t, err)
		contract.shareMsgs[i], err = encryptShareMessages(pubs, ss)
		require.NoError(t, err)
		contract.sharePVSSes[i] = sPvss
		contract.reshareMsgs[i], err = encryptShareMessages(pubs, rss)
		require.NoError(t, err)
		contract.resharePVSSes[i] = rPvss
	}
	// Send resharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretReshare(i+1, j+1, contract.reshareMsgs[j], contract.resharePVSSes[j])
			require.NoError(t, err)
			err = kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	cmt = new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	// Check sharing and resharing
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
		require.NoError(t, err)
	}
}

func TestGroupChange(t *testing.T) {
	dir := t.TempDir()
	// Init keystores
	pubs := make([]*ecies.PublicKey, size+1)
	addrs := make([]common.Address, size+1)
	kss := make([]*KeyStore, size+1)
	for i := 0; i < size+1; i++ {
		key, _ := crypto.HexToECDSA(accounts[i].msgPrivKey)
		pubs[i] = &ecies.ImportECDSA(key).PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(accounts[i].addr, ecies.ImportECDSA(key), size, threshold, accounts[i].pwd)
		require.NoError(t, err)
		addrs[i] = accounts[i].addr
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
		kss[i].OnSharePeriodStart(false)
		// Sharing members, i ranges from 0 to 6
		if i < size {
			ss, pvss, err := kss[i].DKGShare()
			require.NoError(t, err)
			contract.shareMsgs[i], err = encryptShareMessages(pubs[:size], ss)
			require.NoError(t, err)
			contract.sharePVSSes[i] = pvss
		}
		// Not a member, do nothing, i is 7
	}
	// Send secret sharing messages, only broadcast to sharing nodes
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	cmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	// Finalize dkg
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
		require.NoError(t, err)
	}
	err := kss[7].OnEpochChange(nil, encodePointG1(cmt), nil, false)
	require.NoError(t, err)
	// Execute resharing this time
	for i := 0; i < len(addrs); i++ {
		kss[i].OnSharePeriodStart(false)
		// Resharing members
		if i < size {
			rss, rPvss, err := kss[i].DKGReshare()
			require.NoError(t, err)
			contract.reshareMsgs[i], err = encryptShareMessages(pubs[1:], rss)
			require.NoError(t, err)
			contract.resharePVSSes[i] = rPvss
		}
		if i > 0 {
			ss, sPvss, err := kss[i].DKGShare()
			require.NoError(t, err)
			contract.shareMsgs[i-1], err = encryptShareMessages(pubs[1:], ss)
			require.NoError(t, err)
			contract.sharePVSSes[i-1] = sPvss
		}
	}
	// Send resharing messages, only broadcast to sharing nodes, i ranges from 1 to 7
	for i := 1; i < len(addrs); i++ {
		for j := 0; j < size; j++ {
			// Messages from node 0~6 to node 1~7
			err := kss[i].ReceiveSecretReshare(i, j+1, contract.reshareMsgs[j], contract.resharePVSSes[j])
			require.NoError(t, err)
			// Messages from node 1~7 to node 1~7
			err = kss[i].ReceiveSecretShare(i, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	newCmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		newCmt = new(bls12381.G1Affine).Add(newCmt, pg1)
	}
	// Check sharing and resharing
	err = kss[0].OnEpochChange(nil, encodePointG1(newCmt), encodePointG1(cmt), false)
	require.NoError(t, err)
	for i := 1; i <= size; i++ {
		err := kss[i].OnEpochChange(contract.sharePVSSes[i-1], encodePointG1(newCmt), encodePointG1(cmt), i != 0)
		require.NoError(t, err)
	}
}

func TestRecover(t *testing.T) {
	dir := t.TempDir()
	// Init keystores
	pubs := make([]*ecies.PublicKey, size+1)
	addrs := make([]common.Address, size+1)
	kss := make([]*KeyStore, size+1)
	for i := 0; i < size+1; i++ {
		key, _ := crypto.HexToECDSA(accounts[i].msgPrivKey)
		pubs[i] = &ecies.ImportECDSA(key).PublicKey
		ks := NewKeyStore(filepath.Join(dir, "antimev-keystore"+fmt.Sprint(i)))
		err := ks.Init(accounts[i].addr, ecies.ImportECDSA(key), size, threshold, accounts[i].pwd)
		require.NoError(t, err)
		addrs[i] = accounts[i].addr
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
		kss[i].OnSharePeriodStart(false)
		// Sharing members, i ranges from 0 to 6
		if i < size {
			ss, pvss, err := kss[i].DKGShare()
			require.NoError(t, err)
			contract.shareMsgs[i], err = encryptShareMessages(pubs[:size], ss)
			require.NoError(t, err)
			contract.sharePVSSes[i] = pvss
		}
		// Not a member, do nothing, i is 7
	}
	// Send secret sharing messages, only broadcast to sharing nodes
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Aggregate pvss manually
	cmt := new(bls12381.G1Affine).ScalarMultiplicationBase(big.NewInt(0))
	for i := 0; i < size; i++ {
		p, err := new(tpke.PVSS).Decode(contract.sharePVSSes[i], size, threshold)
		require.NoError(t, err)
		pg1, err := decodePointG1(p.GetCommitment().Encode()[:128])
		require.NoError(t, err)
		cmt = new(bls12381.G1Affine).Add(cmt, pg1)
	}
	// Finalize dkg
	for i := 0; i < size; i++ {
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
		require.NoError(t, err)
	}
	err := kss[7].OnEpochChange(nil, encodePointG1(cmt), nil, false)
	require.NoError(t, err)
	// Execute resharing this time
	for i := 0; i < len(addrs); i++ {
		kss[i].OnSharePeriodStart(false)
		// Resharing members
		if i < size {
			rss, rPvss, err := kss[i].DKGReshare()
			require.NoError(t, err)
			contract.reshareMsgs[i], err = encryptShareMessages(pubs[1:], rss)
			require.NoError(t, err)
			contract.resharePVSSes[i] = rPvss
		}
	}
	// Send resharing messages, expect which from validator 7
	for i := 1; i < len(addrs); i++ {
		for j := 0; j < size-1; j++ {
			err := kss[i].ReceiveSecretReshare(i, j+1, contract.reshareMsgs[j], contract.resharePVSSes[j])
			require.NoError(t, err)
		}
	}
	// Execute recovering this time, dead index 7, recover to validator 8
	rIdxs := []int{7}
	rPubs := []*ecies.PublicKey{pubs[7]}
	for i := 0; i < len(addrs); i++ {
		kss[i].OnRecoverPeriodStart()
		require.NoError(t, err)
		if i < 7 {
			ss, err := kss[i].DKGRecover(rIdxs)
			require.NoError(t, err)
			contract.recoverMsgs[i], err = encryptShareMessages(rPubs, ss)
			require.NoError(t, err)
		}
	}
	// Send recover messages, broadcast to all nodes
	for i := 0; i < size-1; i++ {
		err := kss[7].ReceiveRecoverShare(7, i+1, contract.recoverMsgs[i][0], contract.sharePVSSes[size-1])
		require.NoError(t, err)
	}
	// Recover the lost resharing messages
	ss, pvss, err := kss[7].TryRecoverReshare()
	require.NoError(t, err)
	msgs, err := encryptShareMessages(pubs[1:], ss)
	require.NoError(t, err)
	for i := 1; i < len(addrs); i++ {
		err := kss[i].ReceiveSecretReshare(i, 7, msgs, pvss)
		require.NoError(t, err)
	}
	// Only check resharing
	for i := 0; i < len(addrs); i++ {
		err := kss[i].aggregateReshare(encodePointG1(cmt), i != 0)
		require.NoError(t, err)
	}
}

func encodePointG1(p *bls12381.G1Affine) []byte {
	out := make([]byte, 128)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[16:]), p.X)
	fp.BigEndian.PutElement((*[fp.Bytes]byte)(out[64+16:]), p.Y)
	return out
}

func decodePointG1(in []byte) (*bls12381.G1Affine, error) {
	if len(in) != 128 {
		return nil, errors.New("Decode error")
	}
	// decode x
	x, err := decodeBLS12381FieldElement(in[:64])
	if err != nil {
		return nil, err
	}
	// decode y
	y, err := decodeBLS12381FieldElement(in[64:])
	if err != nil {
		return nil, err
	}
	elem := bls12381.G1Affine{X: x, Y: y}
	if !elem.IsOnCurve() {
		return nil, errors.New("Decode error")
	}

	return &elem, nil
}

func decodeBLS12381FieldElement(in []byte) (fp.Element, error) {
	if len(in) != 64 {
		return fp.Element{}, errors.New("Decode error")
	}
	// check top bytes
	for i := 0; i < 16; i++ {
		if in[i] != byte(0x00) {
			return fp.Element{}, errors.New("Decode error")
		}
	}
	var res [48]byte
	copy(res[:], in[16:])

	return fp.BigEndian.Element(&res)
}

func encryptShareMessages(pubs []*ecies.PublicKey, shares []*big.Int) ([][]byte, error) {
	if len(pubs) != len(shares) {
		panic("implementation bug")
	}
	msgs := make([][]byte, len(pubs))
	var err error
	for i, s := range shares {
		msgs[i], err = encryptShareMessage(pubs[i], s)
		if err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

func encryptShareMessage(pub *ecies.PublicKey, share *big.Int) ([]byte, error) {
	nonce, ess, _, bigR, err := encryption.ECIESEncrypt(pub, share.Bytes())
	if err != nil {
		return nil, err
	}
	bigRBytes := bigR.RawBytes()
	// len(message)=64+12+len(ess)
	msg := make([]byte, 0)
	msg = append(msg, bigRBytes[:]...)
	msg = append(msg, nonce...)
	msg = append(msg, ess...)
	return msg, nil
}
