package light

import (
	"bytes"
	"math/big"
	"sort"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft"
	"github.com/ethereum/go-ethereum/consensus/dbft/dbftutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"golang.org/x/crypto/sha3"
)

// VerifyHeaders performs a light verification of the given headers, which
// only checks the signatures and NextConsensus, if the signature is valid, then
// we believe the header is acknowledged by trusted nodes and thus is valid.
// Ref https://github.com/bane-labs/go-ethereum/issues/453.
func VerifyHeaders(headers []*types.Header) bool {
	if len(headers) < 2 {
		return true
	}
	for i, current := range headers[1:] {
		parent := headers[i]
		// Check basic
		if current.ParentHash != parent.Hash() {
			return false
		}
		if current.Number.Cmp(new(big.Int).Add(parent.Number, big.NewInt(1))) != 0 {
			return false
		}
		if current.Time <= parent.Time {
			return false
		}
		expectConsensus := parent.MixDigest
		extra := dbftutil.Extra(current.Extra)
		if len(extra) < 1 {
			return false
		}

		switch extra.Version() {
		case dbftutil.ExtraV0:
			// Check format
			if len(extra) != dbftutil.HashableExtraV0Len+7*common.AddressLength+5*crypto.SignatureLength {
				return false
			}
			// Get CNs and sigs
			addrBytes := extra[dbftutil.HashableExtraV0Len : dbftutil.HashableExtraV0Len+7*common.AddressLength]
			sigBytes := extra[dbftutil.HashableExtraV0Len+7*common.AddressLength:]
			addrs := make([]common.Address, 7)
			for i := range addrs {
				copy(addrs[i][:], addrBytes[i*common.AddressLength:(i+1)*common.AddressLength])
			}
			sigs := make([][]byte, 5)
			for i := range sigs {
				sigs[i] = sigBytes[i*crypto.SignatureLength : (i+1)*crypto.SignatureLength]
			}
			// Verify CNs
			exactConsensus := common.BytesToHash(crypto.Keccak256(addrBytes))
			if exactConsensus != expectConsensus {
				return false
			}
			// Get seal hash
			b := new(bytes.Buffer)
			dbft.EncodeSigHeader(b, current)
			hasher := sha3.NewLegacyKeccak256()
			hasher.Write(b.Bytes())
			// Verify sigs
			return verifyMultiSigs(hasher.Sum(nil), sigs, addrs)
		case dbftutil.ExtraV1, dbftutil.ExtraV2:
			if len(extra) < 2 {
				return false
			}
			switch extra.SignatureScheme() {
			case dbftutil.ExtraV1ECDSAScheme:
				// Check format
				if len(extra) != dbftutil.HashableExtraV1Len+7*common.AddressLength+5*crypto.SignatureLength {
					return false
				}
				// Get CNs and sigs
				addrBytes := extra[dbftutil.HashableExtraV1Len : dbftutil.HashableExtraV1Len+7*common.AddressLength]
				sigBytes := extra[dbftutil.HashableExtraV1Len+7*common.AddressLength:]
				addrs := make([]common.Address, 7)
				for i := range addrs {
					copy(addrs[i][:], addrBytes[i*common.AddressLength:(i+1)*common.AddressLength])
				}
				sigs := make([][]byte, 5)
				for i := range sigs {
					sigs[i] = sigBytes[i*crypto.SignatureLength : (i+1)*crypto.SignatureLength]
				}
				// Verify CNs
				exactConsensus := common.BytesToHash(crypto.Keccak256(addrBytes))
				if exactConsensus != expectConsensus {
					return false
				}
				// Get seal hash
				b := new(bytes.Buffer)
				dbft.EncodeSigHeader(b, current)
				hasher := sha3.NewLegacyKeccak256()
				hasher.Write(b.Bytes())
				// Verify sigs
				return verifyMultiSigs(hasher.Sum(nil), sigs, addrs)
			case dbftutil.ExtraV1ThresholdScheme:
				// Check format
				if len(extra) != dbftutil.HashableExtraV1Len+tpke.PublicKeyLen+tpke.SignatureLen {
					return false
				}
				// Get global public key and sig
				pubBytes := extra[dbftutil.HashableExtraV1Len : dbftutil.HashableExtraV1Len+tpke.PublicKeyLen]
				sigBytes := extra[dbftutil.HashableExtraV1Len+tpke.PublicKeyLen : dbftutil.HashableExtraV1Len+tpke.PublicKeyLen+tpke.SignatureLen]
				pk := new(bls12381.G1Affine)
				_, err := pk.SetBytes(pubBytes)
				if err != nil {
					return false
				}
				sig := new(bls12381.G2Affine)
				_, err = sig.SetBytes(sigBytes)
				if err != nil {
					return false
				}
				// Verify global public key
				exactConsensus := common.BytesToHash(crypto.Keccak256(pubBytes))
				if exactConsensus != expectConsensus {
					return false
				}
				// Get seal hash
				b := new(bytes.Buffer)
				dbft.EncodeSigHeader(b, current)
				hash, _ := bls12381.HashToG2(b.Bytes(), tpke.Domain)
				// Negate the sig in V1
				if extra.Version() == dbftutil.ExtraV1 {
					sig.Neg(sig)
				}
				// Verify sig
				return verifyBLSSig(hash, sig, pk)
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func verifyMultiSigs(hash []byte, sigs [][]byte, addrs []common.Address) bool {
	signers := make([]common.Address, len(sigs))
	for i := range signers {
		pubkey, err := crypto.Ecrecover(hash, sigs[i])
		if err != nil {
			return false
		}
		signers[i] = crypto.PubkeyBytesToAddress(pubkey)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i][:], addrs[j][:]) < 0
	})
	var vi int
	for si := range signers {
		var match bool
		for vi < len(addrs) {
			if addrs[vi] == signers[si] {
				match = true
			}
			vi++
			if match {
				break
			}
		}
		if !match {
			return false
		}
	}
	return true
}

func verifyBLSSig(hash bls12381.G2Affine, sig *bls12381.G2Affine, pub *bls12381.G1Affine) bool {
	_, _, g1, _ := bls12381.Generators()
	g1.Neg(&g1)
	// e(pk,g2Hash)=e(g1,sig)
	ok, err := bls12381.PairingCheck([]bls12381.G1Affine{*pub, g1}, []bls12381.G2Affine{hash, *sig})
	if err != nil {
		return false
	}
	return ok
}
