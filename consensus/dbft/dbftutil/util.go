package dbftutil

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// GetNextConsensusHash returns hash of the given next consensus members.
func GetNextConsensusHash(nextBlockVals []common.Address) common.Hash {
	var res []byte
	for _, v := range nextBlockVals {
		res = append(res, v.Bytes()...)
	}
	return common.BytesToHash(crypto.Keccak256(res))
}
