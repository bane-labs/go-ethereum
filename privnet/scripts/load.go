package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	// loadInterval is a time interval between subsequent transactions batch sending.
	loadInterval = time.Second * 15
	// txBatchSize is a number of transactions per batch.
	txBatchSize = 10
)

var (
	// RPC endpoints.
	rpc1 = "http://localhost:8552"
	rpc5 = "http://localhost:8556"

	// Wallet data.
	keystore1 = "./privnet/four/node1/keystore/UTC--2023-12-05T08-14-15.728267292Z--d4d71adea0eeb18568fecf0fae0810a8520b438b"
	pass1     = "b3Qx3RxWEFEFbQ5dRtaeEzeBRXR4rasV"

	// Nodes addresses.
	node1         = common.HexToAddress("d4d71adea0eeb18568fecf0fae0810a8520b438b")
	node2         = common.HexToAddress("be0c8b1251697f919af32a42b7892b13026433b8")
	node3         = common.HexToAddress("a6b78921e12d527e13a3907da9d6a1fd8e6f8d0a")
	node4         = common.HexToAddress("021d70769889c46db0a8b0b22245c7ffd956d343")
	nodeAddresses = []common.Address{node1, node2, node3, node4}
)

func main() {
	rpcC1, err := rpc.Dial(rpc1)
	check(err)
	c := ethclient.NewClient(rpcC1)
	ctx := context.Background()

	chainID, err := c.ChainID(ctx)
	check(err)

	ks1, err := os.Open(keystore1)
	check(err)
	opts, err := bind.NewTransactorWithChainID(ks1, pass1, chainID)
	check(err)

	var (
		gasFeeCap        = big.NewInt(1000)
		gasTipCap        = big.NewInt(500)
		gasLimit  uint64 = 30000
		value            = big.NewInt(250)
	)
	gasPrice, err := c.SuggestGasPrice(ctx)
	check(err)

	opts.Value = value
	opts.GasFeeCap = gasFeeCap
	opts.GasTipCap = gasTipCap
	opts.GasLimit = gasLimit

	var batchCnt int
	for {
		nonce, err := c.PendingNonceAt(ctx, opts.From)
		check(err)
		var sentCnt int
		for i := 0; i < txBatchSize; i++ {
			baseTx := &types.LegacyTx{
				To:       &nodeAddresses[i%4],
				Nonce:    nonce,
				GasPrice: gasPrice,
				Gas:      gasLimit,
				Value:    value,
				Data:     nil,
			}
			rawTx := types.NewTx(baseTx)
			signedTx, err := opts.Signer(opts.From, rawTx)
			check(err)

			err = c.SendTransaction(ctx, signedTx)
			if err == nil {
				sentCnt++
			}
			nonce++
		}
		batchCnt++
		fmt.Printf("Batch #%d:\n\t%d transactions sent\n\tlast nonce %d\n", batchCnt, sentCnt, nonce-1)
		time.Sleep(loadInterval)
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
