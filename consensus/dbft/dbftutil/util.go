package dbftutil

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

// ExtraVersion denotes a version of block's Extra field. The content of Extra depends
// on this field.
type ExtraVersion byte

const (
	// ExtraVersionLen is a fixed number of extra-data prefix bytes reserved for dFBT block's
	// Extra versioning.
	ExtraVersionLen = 1
	// ExtraV0 is the zero version of block's Extra. Extra of this version includes sorted
	// list of validators addresses followed by BFT number validators signatures.
	ExtraV0 ExtraVersion = 0x00
	// ExtraV1 is the 1-st version of block's Extra. Extra of this version includes global
	// TPKE public key followed by aggregated validators' threshold signature.
	ExtraV1 ExtraVersion = 0x01
	// ExtraV1Fix is the fix of the 1-st version of block's Extra. Extra of this version
	// includes global TPKE public key followed by a fixed V1 threshold signature.
	ExtraV1Fix ExtraVersion = 0x02
)

// ExtraV1SignatureScheme is a scheme of block signature (ECDSA multisignature or
// threshold signature) that is used for ExtraV1 extra.
type ExtraV1SignatureScheme byte

const (
	// ExtraV1SignatureSchemeLen is the length of block signing scheme version for
	// ExtraV1 extra.
	ExtraV1SignatureSchemeLen = 1
	// ExtraV1ECDSAScheme denotes fallback ECDSA multisignature block signing scheme
	// for ExtraV1 extra.
	ExtraV1ECDSAScheme ExtraV1SignatureScheme = 0x00
	// ExtraV1ThresholdScheme denotes primary threshold signature block signing scheme
	// for ExtraV1 extra.
	ExtraV1ThresholdScheme ExtraV1SignatureScheme = 0x01
)

// Constants denoting length of hashable part of block extra for different extra
// versions.
const (
	// HashableExtraV0Len is the length of hashable part of block extra data for
	// ExtraV0 extra version.
	HashableExtraV0Len = ExtraVersionLen
	// HashableExtraV1Len is the length of hashable part of block extra data for
	// ExtraV1 extra version.
	HashableExtraV1Len = ExtraVersionLen + ExtraV1SignatureSchemeLen + common.HashLength // signing version byte + fallback NextConsensus address
)

var (
	// ErrUnexpectedExtraLen is returned if a block's extra-data section doesn't have
	// expected length.
	ErrUnexpectedExtraLen = errors.New("extra len is invalid")

	// ErrUnexpectedExtraVersion is returned when block's Extra version is unknown or
	// unexpected.
	ErrUnexpectedExtraVersion = errors.New("unexpected extra version")

	// ErrUnexpectedBlockSignatureScheme is returned when block's signing scheme is
	// unknown or unexpected.
	ErrUnexpectedBlockSignatureScheme = errors.New("unexpected block signing scheme for V1 extra")
)

// Extra is a type providing versioning extension methods over block extra data.
type Extra []byte

// Version returns version of block extra.
func (e Extra) Version() ExtraVersion {
	return ExtraVersion(e[0])
}

// SignatureScheme returns version of block signature for ExtraV1. It's no-op to apply
// this method to non-V1 extra.
func (e Extra) SignatureScheme() ExtraV1SignatureScheme {
	return ExtraV1SignatureScheme(e[1])
}

// ECDSASigners returns ECDSA multisignature signers and signatures from Extra depending
// on the number of validators.
func (e Extra) ECDSASigners(n int) ([]common.Address, [][]byte, error) {
	if e.Version() != ExtraV0 && e.SignatureScheme() != ExtraV1ECDSAScheme {
		return nil, nil, fmt.Errorf("ECDSA signers can't be recovered for threshold-based block signature scheme")
	}
	var buf []byte
	switch e.Version() {
	case ExtraV0:
		buf = e[HashableExtraV0Len:]
	case ExtraV1, ExtraV1Fix:
		buf = e[HashableExtraV1Len:]
	default:
		return nil, nil, fmt.Errorf("%w: %d", ErrUnexpectedExtraVersion, e.Version())
	}
	// Retrieve the signature from the header extra-data
	var (
		m             = crypto.GetBFTHonestNodeCount(n)
		addrsBytesLen = common.AddressLength * n
		sigsBytesLen  = crypto.SignatureLength * m
		addrs         = make([]common.Address, n)
		sigs          = make([][]byte, m)
	)

	if len(buf) != addrsBytesLen+sigsBytesLen {
		return nil, nil, fmt.Errorf("invalid extra unhashable size: expected %d, got %d", addrsBytesLen+sigsBytesLen, len(buf))
	}
	// Recover Ethereum addresses of validators and their signatures, preserve
	// the order that was specified in the source extra, because validators are
	// sorted and NextConsensus depends on it.
	for i := range addrs {
		addrOffset := i * common.AddressLength
		copy(addrs[i][:], buf[addrOffset:addrOffset+common.AddressLength])
	}
	for i := range sigs {
		sigOffset := len(buf) - sigsBytesLen + i*crypto.SignatureLength
		sigs[i] = buf[sigOffset : sigOffset+crypto.SignatureLength]
	}
	return addrs, sigs, nil
}

// ThresholdSigners returns global public key and threshold signature.
func (e Extra) ThresholdSigners() (*tpke.PublicKey, *tpke.Signature, error) {
	// Sanity check.
	if v := e.Version(); v != ExtraV1 && v != ExtraV1Fix {
		return nil, nil, fmt.Errorf("%w: expected %d or %d, got %d", ErrUnexpectedExtraVersion, ExtraV1, ExtraV1Fix, v)
	}
	if ss := e.SignatureScheme(); ss != ExtraV1ThresholdScheme {
		return nil, nil, fmt.Errorf("%w: expected %d, got %d", ErrUnexpectedBlockSignatureScheme, ExtraV1ThresholdScheme, ss)
	}

	if len(e) != HashableExtraV1Len+tpke.PublicKeyLen+tpke.SignatureLen {
		return nil, nil, fmt.Errorf("%w: %d", ErrUnexpectedExtraLen, len(e))
	}
	pubOffset := HashableExtraV1Len
	// Recover global public key and threshold signature.
	pub, err := tpke.NewPublicKeyFromBytes(e[pubOffset : pubOffset+tpke.PublicKeyLen])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode public key: %w", err)
	}
	sigOffset := len(e) - tpke.SignatureLen
	sig, err := tpke.NewSignatureFromBytes(e[sigOffset : sigOffset+tpke.SignatureLen])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode signature: %w", err)
	}

	return pub, sig, nil
}

// Encodable represents a minimum sufficient interface required from public consensus
// node identifier to construct next consensus address.
type Encodable interface {
	Bytes() []byte
}

// GetNextConsensusHash returns hash of the given next consensus members. nextBlockVals
// must be sorted by their consensus weight.
func GetNextConsensusHash[T Encodable](nextBlockVals []T) common.Hash {
	return common.BytesToHash(crypto.Keccak256(FlattenAddresses(nextBlockVals)))
}

// FlattenAddresses flattens provided addresses in a byte raw.
func FlattenAddresses[T Encodable](vals []T) []byte {
	var res []byte
	for _, v := range vals {
		res = append(res, v.Bytes()...)
	}
	return res
}
