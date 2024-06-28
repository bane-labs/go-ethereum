package encryption

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const ENC_RECEIVER = "0x1212000000000000000000000000000000000003"

func IsEncReceiver(to *common.Address) bool {
	return to != nil && *(to) == common.HexToAddress(ENC_RECEIVER)
}

func IsEncTx(tx *types.Transaction) bool {
	return tx != nil && tx.To() != nil && *(tx.To()) == common.HexToAddress(ENC_RECEIVER)
}
