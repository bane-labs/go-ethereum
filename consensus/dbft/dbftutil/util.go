package dbftutil

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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
)

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
