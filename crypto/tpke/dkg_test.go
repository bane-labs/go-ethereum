package tpke

import (
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/bane-labs/zk-dkg/helper"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/secp256k1"
	"github.com/consensys/gnark-crypto/ecc/secp256k1/fp"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestReplayPVSS(t *testing.T) {
	size := 7
	// Recover the secret
	p0, err := hex.DecodeString("2310dcf245532896eb4edbb8606652ad68a35dda6f59c4d5563dc0f59de3da25")
	require.NoError(t, err)
	p1, err := hex.DecodeString("563d28632595036cc16849ca2dac9680676a3d1191ce632ef8d6f848fa97fe98")
	require.NoError(t, err)
	p2, err := hex.DecodeString("53432267ec5f295cc8a11b357a85f8fe38f6e10297958dca1cf3db66926e518f")
	require.NoError(t, err)
	p3, err := hex.DecodeString("015d482fc85fccb573963895bf074ccbebb3f867f2728ee7be39832390556179")
	require.NoError(t, err)
	p4, err := hex.DecodeString("51d24f8098760707c0ef8f51330d2ebbb8c7091d18a51ebcc63ce76da5a68a46")
	require.NoError(t, err)
	secret := &Secret{
		&Poly{
			coeff: []*big.Int{
				new(big.Int).SetBytes(p0),
				new(big.Int).SetBytes(p1),
				new(big.Int).SetBytes(p2),
				new(big.Int).SetBytes(p3),
				new(big.Int).SetBytes(p4),
			},
		},
	}
	// Recover fis and bigfis
	f := make([]*big.Int, size)
	bigf := make([]*bls12381.G1Affine, size)
	for i := 0; i < size; i++ {
		// Start from 1
		fr := big.NewInt(int64(i + 1))
		// Compute secret share f(i)
		f[i] = secret.poly.evaluate(fr)
		// Compute public share F(i)=f(i)*G1
		bigf[i] = secret.poly.commitment().evaluate(fr)
	}
	// Mock a pvss
	pvss := &PVSS{
		commitment: secret.Commitment(),
		r1:         &bls12381.G1Affine{},
		r2:         &bls12381.G2Affine{},
		bigf:       bigf,
	}
	t.Log(hex.EncodeToString(pvss.Encode()))
}

func TestReplayPubHash(t *testing.T) {
	size := 7
	// Recover the secret
	p0, err := hex.DecodeString("704e6038571bcd1c8cfac37e821fa49b8f6eee724b00156a229fd69975e30933")
	require.NoError(t, err)
	p1, err := hex.DecodeString("1e77a91ff4d3ba13a28735bf62ae1674ed5998292d7cb377795a0adad8a62722")
	require.NoError(t, err)
	p2, err := hex.DecodeString("1330b2da72d82769dc79d97e00b086a15e141d1c77ff850a7a1e914c5b9c2a13")
	require.NoError(t, err)
	p3, err := hex.DecodeString("03602890fda196e706dd80dbade0f1974b414dfe1de130e0b8d6a58b58406198")
	require.NoError(t, err)
	p4, err := hex.DecodeString("3f6350851afa50d28c42258c57eef300eab118099a8e140dcf77dc0203bde12e")
	require.NoError(t, err)
	secret := &Secret{
		&Poly{
			coeff: []*big.Int{
				new(big.Int).SetBytes(p0),
				new(big.Int).SetBytes(p1),
				new(big.Int).SetBytes(p2),
				new(big.Int).SetBytes(p3),
				new(big.Int).SetBytes(p4),
			},
		},
	}
	// Recover fis and bigfis
	f := make([]*big.Int, size)
	bigf := make([]*bls12381.G1Affine, size)
	for i := 0; i < size; i++ {
		// Start from 1
		fr := big.NewInt(int64(i + 1))
		// Compute secret share f(i)
		f[i] = secret.poly.evaluate(fr)
		// Compute public share F(i)=f(i)*G1
		bigf[i] = secret.poly.commitment().evaluate(fr)
	}
	// Pubkeys
	pk0d, err := hex.DecodeString("0478f0364737805e40fdfa9467c3587edc544fb892c8bb7c8a50dc0075619ba8733512bafd28bbe6ce92d1d07be2441c14bbca7525f195084f0d339932f31ee8fa")
	require.NoError(t, err)
	pk0, err := crypto.UnmarshalPubkey(pk0d)
	require.NoError(t, err)
	pk1d, err := hex.DecodeString("04104ac785961b57b00b757d4d3803917b863e0beaae1ed1a3e00fcb0d79ffb53cc45f8d7e491d8d8f0d2373b67458b2a4e711560fbaefa97c28c294da650a76db")
	require.NoError(t, err)
	pk1, err := crypto.UnmarshalPubkey(pk1d)
	require.NoError(t, err)
	pk2d, err := hex.DecodeString("043cebeacd5a001c2143c001e9cd1c67988f928431ccb2d5c7bf64df761b4e3715603685a81fc864826639a9fff2359568170d80ec16ebec2b849c6ba6a9703f6a")
	require.NoError(t, err)
	pk2, err := crypto.UnmarshalPubkey(pk2d)
	require.NoError(t, err)
	pk3d, err := hex.DecodeString("047e29b1bbeba5178dc93c11db9b70955124c520d7bbf45ccf9300eb17254b78cae9f7b5c574a47868909b755c9fe955783a73cd0289556e1d7642c3e7636d259f")
	require.NoError(t, err)
	pk3, err := crypto.UnmarshalPubkey(pk3d)
	require.NoError(t, err)
	pk4d, err := hex.DecodeString("04740defa1c5bd96d5b5a52d0a74cc2e4339690ba8b9dd76719fd699734a6db7579a10f28e4af3af1a2ad0148279b6272f89d0a570d93b47a8650bf9b75a3c3ee9")
	require.NoError(t, err)
	pk4, err := crypto.UnmarshalPubkey(pk4d)
	require.NoError(t, err)
	pk5d, err := hex.DecodeString("040c2103716580b3c2e5c0182a11d004a87563a3fc9162bb8c961fe6c1e5b2209738852359b536226a6cd6980d89a4df08f0e4b0dcdd6d34df07c517aed479cc01")
	require.NoError(t, err)
	pk5, err := crypto.UnmarshalPubkey(pk5d)
	require.NoError(t, err)
	pk6d, err := hex.DecodeString("046a2fbd1db59ca0769c1f29db0fe5053fcc961f1f06a05308489491f4a33dcafb3a8e2e55d067cfd2407bc54b4bcc282143f72364f3b880fd96c6f7a9ccb66bed")
	require.NoError(t, err)
	pk6, err := crypto.UnmarshalPubkey(pk6d)
	require.NoError(t, err)
	pubkeys := []*ecdsa.PublicKey{
		pk0,
		pk1,
		pk2,
		pk3,
		pk4,
		pk5,
		pk6,
	}
	// Others
	m0, err := hex.DecodeString("178c1d7aacb14298ab694f302c5c78de3df6b0c575ca860086dc35298ab47b9c57bef924313ef28316a970f693955f83d68cd544d0b55a58b56c6be30009ca8aca786a78e7422577b09f3ed2fe702e5021d7911aa99d679f9639faa687809028fe2d1ebb3d626f4b841d62e6a4b0ae28592ac0012952c6369bddd7e2")
	require.NoError(t, err)
	m1, err := hex.DecodeString("87d41deab4c6b2f1ed84951f6c7bcf059677ced837391015bf634babb8a193b919d919dacef2ca04c8f7b112c3d2c4cd25790f3567a7aa9c86b184fa5a93c952d0d15dec60c1353cbe4bcab9ac42f9bd28f776cdd64554bf55a641c691815a672b6faed6b7892ebe06e6df891edcd0c43fe743865a297f0cba2aaa4f")
	require.NoError(t, err)
	m2, err := hex.DecodeString("f0e1efc3746da11b5a25080ee1d8b6df77dec37277319289021e850fd11347e9a92e24114aff7a34f18d7d7e02dc43d2299729e7a424ad84cb70ad9e5ed487cc0ef046cebfcebe61672a98c3c15f75337111dca49cdee163be0d5839d9d6ab8665fa858adc4ea3df77961818e6f13067e3af448c606d65a86fe1d59e")
	require.NoError(t, err)
	m3, err := hex.DecodeString("efc1a2a390dcdab2a4330dcb5c3b0eae9086592f61b43c30262badf02cd2db73ecd6d1612e7a2bcd0d03c423d7d3df3c8e432588c679dd7b9d8b46753f7c6d007bf79a5b5a4f362a4f1be9e0be48a529610b09807f9e64d1d0e9686db8f210b36262bde4d6614fb7aeea634c2f4335c7bd7369de8375b59865cddb0a")
	require.NoError(t, err)
	m4, err := hex.DecodeString("729565b8e3c18b7fb91261c5edd239b6989b072fcc011d59d3031506ba2184eecd43e9cf04db32f06db8bed4c83e6d92311373f0bfa7b896886d592e11775ac77c688037274380f62ffb914110fb608dda036f4d46c9e82852ee275ba139c6c5c8d49b02c03c8d7114f81af310ee8b585c2d65b8ed7adb6c9dab6176")
	require.NoError(t, err)
	m5, err := hex.DecodeString("e1d9a4c0c055992895c91f80b452f6f88ec5c818f72e3cd826528bd3728176c9f82e673c4f10088ec876bc1a2164f46454945cd04b1ff4a0e060d96418fe74d68784af8e83e8c9983ea0358f2345641076a82850f7291d4db076b9e23534cfe8d6392840a686e0c1f8a4c40ba74555e3d91b2d9759b1bc4e8f1cba69")
	require.NoError(t, err)
	m6, err := hex.DecodeString("89bd6bb3c99884197007c7907e6f908f693f898a6e7b70a3a4a6b1cb563db8f88f707dc293ee988f68ce4e8227fce92071a2fa265f043da298f76c4e6d13f5fff0dea6c61b7492f3048d159f12348443d5c3413ef18e41eff437c9045740a3fb1aa763c8bc1eed2927836077d2038d5531a2a8d092d5258cde2e9493")
	require.NoError(t, err)
	messages := [][]byte{
		m0, m1, m2, m3, m4, m5, m6,
	}

	// Compute allHash
	batch := 7
	rawPubInputs := make([]byte, 0)
	for index := 0; index < batch; index++ {
		// Format data
		var px fp.Element
		px.SetBigInt(pubkeys[index].X)
		var py fp.Element
		py.SetBigInt(pubkeys[index].Y)
		pub := secp256k1.G1Affine{
			X: px,
			Y: py,
		}
		// Compute allHash
		secp256k1G1ByteLength := secp256k1.SizeOfG1AffineUncompressed
		bls12381G1ByteLength := bls12381.SizeOfG1AffineUncompressed
		bigRBytes := messages[index][:64]
		rawBigR := make([]byte, secp256k1G1ByteLength)
		for i := 0; i < secp256k1G1ByteLength; i++ {
			rawBigR[i] = bigRBytes[i] // bytes
		}
		pubBytes := pub.RawBytes()
		rawPub := make([]byte, secp256k1G1ByteLength)
		for i := 0; i < secp256k1G1ByteLength; i++ {
			rawPub[i] = pubBytes[i] // bytes
		}
		bigFiBytes := bigf[index].RawBytes()
		rawBigFi := make([]byte, bls12381G1ByteLength)
		for i := 0; i < bls12381G1ByteLength; i++ {
			rawBigFi[i] = bigFiBytes[i] // bytes
		}
		singleHash := helper.GetHash(append(append(append(append(append(rawBigR, rawPub...), rawBigFi...), messages[index][64:76]...), 2), messages[index][76:]...))
		t.Log(hex.EncodeToString(append(append(append(append(append(rawBigR, rawPub...), rawBigFi...), messages[index][64:76]...), 2), messages[index][76:]...)))
		rawPubInputs = append(rawPubInputs, singleHash...)
	}
	sumHash := helper.GetHash(rawPubInputs)
	t.Log(hex.EncodeToString(sumHash))
}
