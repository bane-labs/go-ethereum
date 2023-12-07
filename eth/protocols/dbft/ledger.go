// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"github.com/ethereum/go-ethereum/common"
)

type ledger struct {
	bc BlockChainAPI
}

func newLedger(bc BlockChainAPI) *ledger {
	return &ledger{bc: bc}
}

func (l *ledger) BlockHeight() uint64 {
	return uint64(l.bc.BlockNumber())
}

func (l *ledger) IsAddressAllowed(addr common.Address) bool {
	// Call governance contract here.
	return true
}
