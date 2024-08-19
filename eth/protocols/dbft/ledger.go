// Copyright 2023 NeoSPCC
//
// MIT License.

package dbft

import (
	"github.com/ethereum/go-ethereum/common"
)

type ledger struct {
	bc                  BlockChainAPI
	isExtensibleAllowed func(height uint64, addr common.Address) error
}

func newLedger(bc BlockChainAPI, isExtensibleAllowed func(uint64, common.Address) error) *ledger {
	return &ledger{
		bc:                  bc,
		isExtensibleAllowed: isExtensibleAllowed,
	}
}

func (l *ledger) BlockHeight() uint64 {
	return uint64(l.bc.BlockNumber())
}

func (l *ledger) IsAddressAllowed(addr common.Address) error {
	return l.isExtensibleAllowed(l.BlockHeight(), addr)
}
