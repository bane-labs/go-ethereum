package antimev

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/stretchr/testify/require"
)

// addrIdx is a structure combining CN node address and its index in the privnet setup.
type addrIdx struct {
	addr common.Address
	i    int
}

// privnetCNs stores the set of 7-nodes privnet addresses along with their indexes.
// It's sorted by addresses order, exactly like dBFT validators.
var privnetCNs = []addrIdx{
	{addr: common.HexToAddress("0x74f4effb0b538baec703346b03b6d9292f53a4cd"), i: 1},
	{addr: common.HexToAddress("0x910ad1641b7125eff746accdca1f11148b22f472"), i: 2},
	{addr: common.HexToAddress("0xfef5f250af14df73f983caab7b1f5002189c42e0"), i: 3},
	{addr: common.HexToAddress("0xc51964013acbc6b271feecb0febd9e7a01202930"), i: 4},
	{addr: common.HexToAddress("0xc5bbd9652546bc96be3dec97a38ee335f7873dfa"), i: 5},
	{addr: common.HexToAddress("0x26f1794b81df2b832545b8b6bbca196b82e4feb1"), i: 6},
	{addr: common.HexToAddress("0x0b51369d02e47ee3f143391b837aa08c31aaa19b"), i: 7},
}

func init() {
	slices.SortFunc(privnetCNs, func(a, b addrIdx) int {
		return common.Address.Cmp(a.addr, b.addr)
	})
}

func TestTPKE(t *testing.T) {
	const saveKeystore = false

	privnetCNAddresses := make([]common.Address, len(privnetCNs))
	for i := range privnetCNAddresses {
		privnetCNAddresses[i] = privnetCNs[i].addr
	}

	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)

	pubs := make([]*ecies.PublicKey, size)
	kss := make([]*AMEVKeyStore, size)
	for i := 0; i < size; i++ {
		key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
		pubs[i] = &key.PublicKey
		ks, err := NewKeyStore(privnetCNs[i].addr, key, size, threshold)
		if err != nil {
			t.Fatalf(err.Error())
		}
		kss[i] = ks
	}

	msgbox := make([][][]byte, size)
	pvssbox := make([][]byte, size)
	for i := 0; i < size; i++ {
		// No reshare to handle
		_, _, err := kss[i].OnValidatorList(privnetCNAddresses, pubs)
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

	if saveKeystore {
		for i := range kss {
			ksBytes, err := kss[i].Bytes()
			require.NoError(t, err)
			ks := new(AMEVKeyStore)
			fmt.Printf("%s (CN %d):\n\t%s\n", privnetCNs[i].addr, privnetCNs[i].i, hex.EncodeToString(ksBytes))
			os.WriteFile(fmt.Sprintf("../../privnet/seven/node%d/amev_keystore.txt", privnetCNs[i].i), []byte(hex.EncodeToString(ksBytes)), os.ModePerm)

			// Double-check to ensure keystore marshalling works correctly, and it's
			// possible to decrypt message with deserialized keystore.
			require.NoError(t, ks.FromBytes(ksBytes))
			kss[i] = ks
		}
	}

	// Encrypt
	msg := []byte("some data that is more than 105 bytes in length: pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	encryptedKey, encryptedMsg, err := kss[0].Encrypt(msg)
	if err != nil {
		t.Fatalf(err.Error())
	}

	if saveKeystore {
		var envelopeData = []byte{0xff, 0xff, 0xff, 0xff}
		envelopeData = append(envelopeData, encryptedKey.ToBytes()...)
		envelopeData = append(envelopeData, encryptedMsg...)
		fmt.Printf("encryptedKey: %s\nencryptedMsg: %s\nEnvelope's data: '0x%s'", hex.EncodeToString(encryptedKey.ToBytes()), hex.EncodeToString(encryptedMsg), hex.EncodeToString(envelopeData))
	}

	// Verify ciphertext
	if err := encryptedKey.Verify(); err != nil {
		t.Fatalf("invalid ciphertext.")
	}

	// Generate shares
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < size-2; i++ {
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

func TestBenchmark(t *testing.T) {
	sampleAmount := 1500
	size := 7
	threshold := 5

	// DKG
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
	if err != nil {
		t.Fatalf(err.Error())
	}

	// Generate shares
	shares := make(map[int][]*tpke.DecryptionShare)
	for i := 0; i < size; i++ {
		share, err := kss[i].DecryptWithShare(encryptedSeeds)
		if err != nil {
			t.Fatalf(err.Error())
		}
		shares[i+1] = share
	}

	// Decrypt
	t1 := time.Now()
	results, err := kss[0].AggregateAndDecryptWithShare(encryptedSeeds, encryptedMsgs, shares)
	if err != nil {
		t.Fatalf(err.Error())
	}
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

func parallelEncrypt(index int, ks *AMEVKeyStore, input []byte, ch chan<- message) {
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
