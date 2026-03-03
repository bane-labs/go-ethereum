package dbft

import (
	"github.com/ethereum/go-ethereum/core/types"
)

type FSWriter interface {
	CommitSealBlockHash(block *types.Block) error
}
