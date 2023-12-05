package dbftutil

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// GetNextConsensusHash returns hash of the given next consensus members.
func GetNextConsensusHash(nextBlockVals []common.Address) common.Hash {
	// TODO: we need to decide what's the NextConsensus address in fact.
	var res []byte
	for _, v := range nextBlockVals {
		res = append(res, v.Bytes()...)
	}
	return common.BytesToHash(crypto.Keccak256(res))
}
