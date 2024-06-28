package encryption

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
)

func IsEncReceiver(to *common.Address) bool {
	return to != nil && *(to) == systemcontracts.GovernanceRewardHash
}

func IsEncTx(tx *types.Transaction) bool {
	return tx != nil && IsEncReceiver(tx.To())
}
