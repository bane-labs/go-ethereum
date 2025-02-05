package antimev

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/stretchr/testify/require"
)

func TestTPKE(t *testing.T) {
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
		kss[i].OnSharePeriodStart()
		msgs, pvss, err := kss[i].DKGShare(pubs)
		require.NoError(t, err)
		contract.shareMsgs[i] = msgs
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
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), true)
		require.NoError(t, err)
	}

	// Encrypt
	msg := []byte("some data that is more than 105 bytes in length: pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	encryptedKey, encryptedMsg, err := kss[0].Encrypt(msg)
	require.NoError(t, err)

	// Verify ciphertext
	if err := encryptedKey.Verify(); err != nil {
		t.Fatalf("invalid ciphertext.")
	}

	// Generate an example envelope for privnet verification
	var envelopeData = []byte{0xff, 0xff, 0xff, 0xff}
	envelopeData = append(envelopeData, encryptedKey.ToBytes()...)
	envelopeData = append(envelopeData, encryptedMsg...)
	t.Logf("encryptedKey: %s\nencryptedMsg: %s\nenvelopeData: 0x%s", hex.EncodeToString(encryptedKey.ToBytes()), hex.EncodeToString(encryptedMsg), hex.EncodeToString(envelopeData))

	// Generate shares
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < size-2; i++ {
		share, err := kss[i].DecryptWithShare([]*tpke.CipherText{encryptedKey})
		require.NoError(t, err)
		shares[i+1] = share
	}

	// Decrypt
	results, err := kss[0].AggregateAndDecryptWithShare([]*tpke.CipherText{encryptedKey}, [][]byte{encryptedMsg}, shares)
	require.NoError(t, err)
	for i := 0; i < len(msg); i++ {
		if msg[i] != results[0][i] {
			t.Fatalf("decryption failed.")
		}
	}
}

const regenerate = false

// TestInitKeyStores generates antimev keystores to privnets
func TestInitKeyStores(t *testing.T) {
	if !regenerate {
		return
	}
	sizes := []int{1, 4, 7}
	thresholds := []int{1, 3, 5}
	folders := []string{"single", "four", "seven"}

	for i := range sizes {
		for j := 0; j <= sizes[i]; j++ {
			key, _ := crypto.HexToECDSA(accounts[j].msgPrivKey)
			ks := NewKeyStore(filepath.Join("../privnet/"+folders[i]+"/node"+fmt.Sprint(j), "antimev-keystore"))
			err := ks.Init(accounts[j].addr, ecies.ImportECDSA(key), sizes[i], thresholds[i], accounts[j].pwd)
			require.NoError(t, err)
			err = ks.Persist()
			require.NoError(t, err)
		}
	}
}

// TestGenerateEncryptedTx generates an encrypted transaction using 7-nodes
func TestGenerateEncryptedTx(t *testing.T) {
	// epoch is an epoch of Envelope data encryption. The resulting encrypted transaction can be
	// decrypted only during this or next epoch.
	const epoch = 1
	require.Equal(t, 7, size, "refactor test if different number of CNs is needed")

	// Retrieve and decrypt the set of anti-MEV key storages.
	kss := make([]*KeyStore, size)
	for i := range kss {
		kss[i] = NewKeyStore(filepath.Join("..", "privnet", "seven", fmt.Sprintf("node%d", i+1), "antimev-keystore"))
		require.NoError(t, kss[i].Load(accounts[i+1].pwd))
	}
	tx := buildTransferFromPriv1(t)
	// Encrypt transaction.
	buf := bytes.NewBuffer(nil)
	require.NoError(t, tx.EncodeRLP(buf))
	msg := buf.Bytes()
	encryptedKey, encryptedMsg, err := kss[0].Encrypt(msg)
	require.NoError(t, err)
	// Verify ciphertext.
	if err := encryptedKey.Verify(); err != nil {
		t.Fatalf("invalid ciphertext")
	}
	// Generate envelope.
	var envelopeData = []byte{
		0xff, 0xff, 0xff, 0xff, // version
		0, 0, 0, 0, // epoch
	}
	binary.LittleEndian.PutUint32(envelopeData[4:8], epoch)
	envelopeData = append(envelopeData, encryptedKey.ToBytes()...)
	envelopeData = append(envelopeData, encryptedMsg...)
	t.Logf("encryptedKey: %s\nencryptedMsg: %s\nenvelopeData: 0x%s\nencrypted tx hash: %s\n",
		hex.EncodeToString(encryptedKey.ToBytes()), hex.EncodeToString(encryptedMsg), hex.EncodeToString(envelopeData), tx.Hash())
	// Verify encrypted data are decryptable. Generate shares.
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < threshold; i++ {
		share, err := kss[i].DecryptWithShare([]*tpke.CipherText{encryptedKey})
		require.NoError(t, err)
		shares[i+1] = share
	}
	// Decrypt and check that it's the same message.
	results, err := kss[0].AggregateAndDecryptWithShare([]*tpke.CipherText{encryptedKey}, [][]byte{encryptedMsg}, shares)
	require.NoError(t, err)
	require.Equal(t, 1, len(results))
	require.NotNil(t, results[0])
	require.True(t, bytes.Equal(results[0], msg), hex.EncodeToString(results[0]), hex.EncodeToString(msg))
}

// buildTransferFromPriv1 returns a signed transaction that transfers 1 wei from
// node1 to node1 with nonce 0.
func buildTransferFromPriv1(t *testing.T) *types.Transaction {
	ks := keystore.NewKeyStore(filepath.Join("..", "privnet", "seven", "node1", "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	acc := ks.Accounts()[0]
	require.NoError(t, ks.Unlock(acc, accounts[1].pwd))

	// These variables are taken based on experience of previously generated transfer
	// transactions for privnet. This transaction has nonce set to 0, hence it's valid
	// only if it's the first accepted transaction for Priv0.
	var (
		to       = acc.Address               // self-transfer
		nonce    = uint64(0)                 // first transaction for priv0
		gasPrice = big.NewInt(400_0000_0000) // based on (*ethclient.Client).SuggestGasPrice
		gasLimit = uint64(2_1000)            // based on (*ethclient.Client).EstimateGas
		value    = big.NewInt(1)             // 1 wei
	)

	tx := types.NewTx(&types.LegacyTx{
		To:       &to,
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		Value:    value,
		Data:     nil,
	})
	res, err := ks.SignTx(acc, tx, big.NewInt(magic))
	require.NoError(t, err)

	return res
}

func TestBenchmark(t *testing.T) {
	sampleAmount := 1500
	size := 7
	threshold := 5

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
		kss[i].OnSharePeriodStart()
		msgs, pvss, err := kss[i].DKGShare(pubs)
		require.NoError(t, err)
		contract.shareMsgs[i] = msgs
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
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), true)
		require.NoError(t, err)
	}

	// Build a 1MB script
	script := make([]byte, 131072)
	rand.Read(script)
	ch := make(chan message, sampleAmount)

	// Encrypt script
	for i := 0; i < sampleAmount; i++ {
		go parallelEncrypt(i, kss[0], script, ch)
	}
	encryptedSeeds, encryptedMsgs, _ := messageHandler(ch, sampleAmount)

	// Verify encrypted seeds
	for i := 0; i < sampleAmount; i++ {
		go parallelCTVerify(encryptedSeeds[i], ch)
	}
	_, _, err := messageHandler(ch, sampleAmount)
	require.NoError(t, err)

	// Generate shares
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < size; i++ {
		share, err := kss[i].DecryptWithShare(encryptedSeeds)
		require.NoError(t, err)
		shares[i+1] = share
	}

	// Decrypt
	t1 := time.Now()
	results, err := kss[0].AggregateAndDecryptWithShare(encryptedSeeds, encryptedMsgs, shares)
	require.NoError(t, err)
	t.Logf("threshold decryption time: %v", time.Since(t1))
	for i := 0; i < sampleAmount; i++ {
		if !bytes.Equal(results[i], script) {
			t.Fatalf("decryption failed.")
		}
	}
}

type message struct {
	index int
	ck    *tpke.CipherText
	cmsg  []byte
	err   error
}

func parallelCTVerify(ct *tpke.CipherText, ch chan<- message) {
	ch <- message{
		index: 0,
		err:   ct.Verify(),
		ck:    nil,
		cmsg:  nil,
	}
}

func parallelEncrypt(index int, ks *KeyStore, input []byte, ch chan<- message) {
	ck, cmsg, err := ks.Encrypt(input)
	ch <- message{
		index: index,
		ck:    ck,
		cmsg:  cmsg,
		err:   err,
	}
}

func messageHandler(ch <-chan message, amount int) ([]*tpke.CipherText, [][]byte, error) {
	cks := make([]*tpke.CipherText, amount)
	cmsgs := make([][]byte, amount)
	for i := 0; i < amount; i++ {
		msg := <-ch
		if msg.err != nil {
			return nil, nil, msg.err
		}
		cks[msg.index] = msg.ck
		cmsgs[msg.index] = msg.cmsg
	}
	return cks, cmsgs, nil
}
