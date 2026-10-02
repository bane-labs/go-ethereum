package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nspcc-dev/dbft"
)

// We don't use a wrapper of [types.Transaction] since its implementation is
// sufficient for dBFT functioning.
var _ = dbft.Transaction[common.Hash](&types.Transaction{})
