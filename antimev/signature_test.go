package antimev

import (
	"fmt"
	"maps"
	"math/big"
	"path/filepath"
	"slices"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/stretchr/testify/require"
)

func TestSingleSignature(t *testing.T) {
	sk := tpke.RandomPrivateKey()
	pk := sk.GetPublicKey()

	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	share := sk.SignShare(msg, false)
	if !pk.VerifySigShare(msg, share, false) {
		t.Fatalf("invalid signature")
	}
	share = sk.SignShare(msg, true)
	if !pk.VerifySigShare(msg, share, true) {
		t.Fatalf("invalid signature")
	}
}

// Verify against results from https://github.com/ChainSafe/bls
func TestEthBLSSignEquivalence(t *testing.T) {
	sk, err := new(tpke.PrivateKey).FromBytes(common.FromHex("0x0075d54c786f77c983e59d452f933f98a8aba65a4c09fca937dfe15cb46631f1"))
	require.NoError(t, err)
	pk, err := tpke.NewPublicKeyFromBytes(common.FromHex("0xa257e2f600440a678e23f5db21d28ff65682c08ea20ea90a2a25f0a46340ccb893f04bd789a3b65bebaa298c99c2d220"))
	require.NoError(t, err)
	if !pk.Equal(sk.GetPublicKey()) {
		t.Fatalf("invalid keypair")
	}
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	sig := common.FromHex("0x853c6e915ad9176e6af5bc4b4e258f1457175347d72a393725605de7f33160123682a9eb2bb52bb64e8198a2aa01b0f4171d9ff07ab6589a29dfbe1286444739766f253935a7caefea7920e012794bd5420d4c8d3259251e789a28c54f3b6a2e")
	if !slices.Equal(sk.SignShare(msg, false).Bytes(), sig) {
		t.Fatalf("invalid signature")
	}
}

// Test vectors from https://github.com/ethereum/consensus-spec-tests
func TestEthBLSVerifyEquivalence(t *testing.T) {
	pk, err := tpke.NewPublicKeyFromBytes(common.FromHex("0xa491d1b0ecd9bb917989f0e74f0dea0422eac4a873e5e2644f368dffb9a6e20fd6e10c1b77654d067c0618f6e5a7f79a"))
	require.NoError(t, err)
	msg := common.FromHex("0x0000000000000000000000000000000000000000000000000000000000000000")
	sig, err := tpke.NewSignatureFromBytes(common.FromHex("0xb6ed936746e01f8ecf281f020953fbf1f01debd5657c4a383940b020b26507f6076334f91e2366c96e9ab279fb5158090352ea1c5b0c9274504f4f0e7053af24802e51e4568d164fe986834f41e55c8e850ce1f98458c0cfc9ab380b55285a55"))
	require.NoError(t, err)
	if !pk.VerifySig(msg, sig, false) {
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
		ss, pvss, err := kss[i].DKGShare()
		require.NoError(t, err)
		contract.shareMsgs[i] = encryptShareMessages(pubs, ss)
		contract.sharePVSSes[i] = pvss
	}
	// Send secret sharing messages
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			err := kss[i].ReceiveSecretShare(i+1, j+1, contract.shareMsgs[j], contract.sharePVSSes[j])
			if err != nil {
				t.Fatalf(err.Error())
			}
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

	// Test functionality
	msg := []byte("pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza pizza")
	shares := make(map[int]*tpke.SignatureShare)
	for i := 0; i < size; i++ {
		share, err := kss[i].SignShare(msg, false)
		require.NoError(t, err)
		shares[i+1] = share
	}

	sh0 := maps.Clone(shares)
	sh1 := maps.Clone(shares)
	delete(sh0, 1)
	delete(sh1, 2)
	sig, err := kss[0].AggregateAndVerifySig(msg, shares, false)
	require.NoError(t, err)
	require.NotNil(t, sig)
	sig0, err := kss[0].AggregateAndVerifySig(msg, sh0, false)
	require.NoError(t, err)
	sig1, err := kss[0].AggregateAndVerifySig(msg, sh1, false)
	require.NoError(t, err)
	require.Equal(t, sig0, sig1)
}
