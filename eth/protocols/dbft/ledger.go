// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"github.com/ethereum/go-ethereum/common"
)

type ledger struct {
	bc                  BlockChainAPI
	isExtensibleAllowed func(addr common.Address) bool
}

func newLedger(bc BlockChainAPI, isExtensibleAllowed func(common.Address) bool) *ledger {
	return &ledger{
		bc:                  bc,
		isExtensibleAllowed: isExtensibleAllowed,
	}
}

func (l *ledger) BlockHeight() uint64 {
	return uint64(l.bc.BlockNumber())
}

func (l *ledger) IsAddressAllowed(addr common.Address) bool {
	return l.isExtensibleAllowed(addr)
}
