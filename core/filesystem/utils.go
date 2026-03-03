package filesystem

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/params"
)

func BlockNumberToEpoch(blockNumber *big.Int) primitives.Epoch {
	return primitives.Epoch(big.NewInt(0).Div(blockNumber, big.NewInt(params.BlocksPerEthEpoch)).Uint64())
}
