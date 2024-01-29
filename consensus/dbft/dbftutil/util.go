package dbftutil

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// ExtraVersionLen is a fixed number of extra-data prefix bytes reserved for dFBT block's
	// Extra versioning.
	ExtraVersionLen = 1
	// ExtraV0 is the zero version of block's Extra. Extra of this version includes sorted
	// list of validators addresses followed by BFT number validators signatures.
	ExtraV0 = 0x00
)

// GetNextConsensusHash returns hash of the given next consensus members. nextBlockVals
// must be sorted by their consensus weight.
func GetNextConsensusHash(nextBlockVals []common.Address) common.Hash {
	return common.BytesToHash(crypto.Keccak256(FlattenAddresses(nextBlockVals)))
}

// FlattenAddresses flattens provided addresses in a byte raw.
func FlattenAddresses(vals []common.Address) []byte {
	res := make([]byte, len(vals)*common.AddressLength)
	for i, v := range vals {
		offset := i * common.AddressLength
		copy(res[offset:], v.Bytes())
	}
	return res
}
