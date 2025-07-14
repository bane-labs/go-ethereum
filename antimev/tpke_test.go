package antimev

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	coreaccounts "github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/ethclient"
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
		kss[i].OnSharePeriodStart(false)
		ss, pvss, err := kss[i].DKGShare(big.NewInt(1))
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
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
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
	var envelopeData = EncryptedDataPrefix
	envelopeData = binary.BigEndian.AppendUint32(envelopeData, 0)
	envelopeData = binary.BigEndian.AppendUint32(envelopeData, 0)
	envelopeData = append(envelopeData, common.MaxHash[:]...)
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
	const (
		skip = true
		send = false
		// rpc1 is an RPC endpoint address of privnet's CN1.
		rpc1 = "http://localhost:8562"
	)
	if skip {
		t.Skip()
	}
	require.Equal(t, 7, size, "refactor test if different number of CNs is needed")

	// Unlock priv0 account (watch-only CN) to sign both Envelope and encrypted transaction.
	ks := keystore.NewKeyStore(filepath.Join("..", "privnet", "seven", "node0", "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	acc := ks.Accounts()[0]
	require.NoError(t, ks.Unlock(acc, accounts[0].pwd))

	// Retrieve and decrypt the set of anti-MEV key storages.
	kss := make([]*KeyStore, size)
	for i := range kss {
		kss[i] = NewKeyStore(filepath.Join("..", "privnet", "seven", fmt.Sprintf("node%d", i+1), "antimev-keystore"))
		require.NoError(t, kss[i].Load(accounts[i+1].pwd))
	}
	tx := buildTransferFromPriv0(t, ks, acc)
	// Encrypt transaction.
	msg, err := tx.MarshalBinary()
	require.NoError(t, err)
	encryptedKey, encryptedMsg, err := kss[0].Encrypt(msg)
	require.NoError(t, err)
	// Verify ciphertext.
	if err := encryptedKey.Verify(); err != nil {
		t.Fatalf("invalid ciphertext")
	}
	// Generate envelope.
	var envelopeData = EncryptedDataPrefix
	// The resulting encrypted transaction can be decrypted only during this or next epoch.
	envelopeData = binary.BigEndian.AppendUint32(envelopeData, uint32(kss[0].Round()))
	envelopeData = binary.BigEndian.AppendUint32(envelopeData, uint32(tx.Gas()))
	envelopeData = append(envelopeData, tx.Hash().Bytes()...)
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

	// Construct and send Envelope to the RPC node.
	e := buildEnvelopeFromPriv0(t, ks, acc, envelopeData)
	client, err := ethclient.Dial(rpc1)
	require.NoError(t, err)
	require.NoError(t, client.SendTransaction(context.Background(), e))
}

// buildTransferFromPriv0 returns a signed transaction that transfers 1 wei from
// node0 to node0 with nonce 0.
func buildTransferFromPriv0(t *testing.T, ks *keystore.KeyStore, acc coreaccounts.Account) *types.Transaction {
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
	res, err := ks.SignTx(acc, tx, big.NewInt(int64(magic)))
	require.NoError(t, err)

	return res
}

// buildEnvelopeFromPriv0 returns a signed Envelope transaction with the specified data.
func buildEnvelopeFromPriv0(t *testing.T, ks *keystore.KeyStore, acc coreaccounts.Account, data []byte) *types.Transaction {
	// These variables are taken based on experience of previously generated transfer
	// transactions for privnet. This transaction has nonce set to 0, hence it's valid
	// only if it's the first accepted transaction for Priv0.
	var (
		to       = systemcontracts.GovernanceRewardProxyHash // transfer to Governance Reward as required by Envelope verification rules
		nonce    = uint64(0)                                 // same nonce as for Inner transaction
		gasPrice = big.NewInt(400_0000_0000)                 // based on (*ethclient.Client).SuggestGasPrice
		gasLimit = uint64(3_1000)                            // higher than required by inner transaciton
		value    = big.NewInt(1)                             // 1 wei
	)

	tx := types.NewTx(&types.LegacyTx{
		To:       &to,
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		Value:    value,
		Data:     data,
	})
	res, err := ks.SignTx(acc, tx, big.NewInt(int64(magic)))
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
		kss[i].OnSharePeriodStart(false)
		ss, pvss, err := kss[i].DKGShare(big.NewInt(1))
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
		err := kss[i].OnEpochChange(contract.sharePVSSes[i], encodePointG1(cmt), nil, true)
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
