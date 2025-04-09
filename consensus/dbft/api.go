// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package dbft

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
)

// StatisticsConfig is the configuration for dBFT statistics calculation.
type StatisticsConfig struct {
	// DefaultStatisticsPeriod is the default number of blocks to be taken into
	// account during dBFT statistics calculation.
	DefaultStatisticsPeriod uint64
	// MaxStatisticsPeriod is the maximum number of blocks to be taken into
	// account during dBFT statistics calculation.
	MaxStatisticsPeriod uint64
}

// DefaultStatistics is the default dBFT statistics calculation configuration.
var DefaultStatistics = StatisticsConfig{
	DefaultStatisticsPeriod: 64,
	MaxStatisticsPeriod:     1000,
}

// API is a user facing RPC API to allow retrieve some information from the DBFT
// consensus engine.
type API struct {
	chain  consensus.ChainHeaderReader
	bft    *DBFT
	config StatisticsConfig
}

// GetSigners retrieves the list of authorized signers at the specified block.
func (api *API) GetSigners(number *rpc.BlockNumber) ([]common.Address, error) {
	// Retrieve the requested block number (or current if none requested)
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	// Ensure we have an actually valid block and parse the signers from it.
	if header == nil {
		return nil, errUnknownBlock
	}

	// Genesis block doesn't have validators signatures set, only validators addresses
	// are filled in.
	if header.Number.Uint64() == 0 {
		return []common.Address{}, nil
	}

	return api.bft.Signers(header)
}

// GetValidators retrieves the list of block validators.
func (api *API) GetValidators(number *rpc.BlockNumber) ([]common.Address, error) {
	// Retrieve the requested block number (or current if none requested)
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	// Ensure we have an actually valid block and parse the signers from it.
	if header == nil {
		return nil, errUnknownBlock
	}

	return api.bft.Validators(header)
}

// GetSignersAtHash retrieves the list of authorized signers at the specified block.
func (api *API) GetSignersAtHash(hash common.Hash) ([]common.Address, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	// Genesis block doesn't have validators signatures set, only validators addresses
	// are filled in.
	if header.Number.Uint64() == 0 {
		return []common.Address{}, nil
	}
	return api.bft.Signers(header)
}

// GetValidatorsAtHash retrieves the list of block validators.
func (api *API) GetValidatorsAtHash(hash common.Hash) ([]common.Address, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.bft.Validators(header)
}

type status struct {
	InturnPercent float64                `json:"inturnPercent"`
	SigningStatus map[common.Address]int `json:"sealerActivity"`
	NumBlocks     uint64                 `json:"numBlocks"`
}

// Status returns the status of the last N blocks:
// - the number of blocks,
// - the sealer activity,
// - the percentage of in-turn blocks.
func (api *API) Status(n *uint64) (*status, error) {
	var (
		numBlocks = api.config.DefaultStatisticsPeriod
		header    = api.chain.CurrentHeader()
		diff      = uint64(0)
		optimals  = 0
	)
	if n != nil {
		if *n <= api.config.MaxStatisticsPeriod {
			numBlocks = *n
		} else {
			numBlocks = api.config.MaxStatisticsPeriod
		}
	}
	var (
		end   = header.Number.Uint64()
		start = end - numBlocks
	)
	if numBlocks > end {
		start = 1
		numBlocks = end - start
	}
	signStatus := make(map[common.Address]int)
	for n := start; n < end; n++ {
		h := api.chain.GetHeaderByNumber(n)
		if h == nil {
			return nil, fmt.Errorf("missing block %d", n)
		}
		if h.Difficulty.Cmp(diffInTurn) == 0 {
			optimals++
		}
		diff += h.Difficulty.Uint64()
		sealer, err := api.bft.Primary(h)
		if err != nil {
			return nil, err
		}
		signStatus[sealer]++
	}
	return &status{
		InturnPercent: float64(100*optimals) / float64(numBlocks),
		SigningStatus: signStatus,
		NumBlocks:     numBlocks,
	}, nil
}

type blockNumberOrHashOrRLP struct {
	*rpc.BlockNumberOrHash
	RLP hexutil.Bytes `json:"rlp,omitempty"`
}

func (sb *blockNumberOrHashOrRLP) UnmarshalJSON(data []byte) error {
	bnOrHash := new(rpc.BlockNumberOrHash)
	// Try to unmarshal bNrOrHash
	if err := bnOrHash.UnmarshalJSON(data); err == nil {
		sb.BlockNumberOrHash = bnOrHash
		return nil
	}
	// Try to unmarshal RLP
	var input string
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	blob, err := hexutil.Decode(input)
	if err != nil {
		return err
	}
	sb.RLP = blob
	return nil
}

// GetPrimary returns the primary Ethereum address for a specific dBFT block.
// Can be called with a block number, a block hash or a rlp encoded blob.
// The RLP encoded blob can either be a block or a header.
func (api *API) GetPrimary(rlpOrBlockNr *blockNumberOrHashOrRLP) (common.Address, error) {
	header, err := getHeader(rlpOrBlockNr, api.chain)
	if err != nil {
		return common.Address{}, err
	}
	return api.bft.Primary(header)
}

// GetCoinbase returns the beneficiary address for a specific dBFT block.
// Can be called with a block number, a block hash or a rlp encoded blob.
// The RLP encoded blob can either be a block or a header.
func (api *API) GetCoinbase(rlpOrBlockNr *blockNumberOrHashOrRLP) (common.Address, error) {
	header, err := getHeader(rlpOrBlockNr, api.chain)
	if err != nil {
		return common.Address{}, err
	}
	return api.bft.Author(header)
}

func getHeader(rlpOrBlockNr *blockNumberOrHashOrRLP, chain consensus.ChainHeaderReader) (*types.Header, error) {
	if len(rlpOrBlockNr.RLP) == 0 {
		blockNrOrHash := rlpOrBlockNr.BlockNumberOrHash
		var header *types.Header
		if blockNrOrHash == nil {
			header = chain.CurrentHeader()
		} else if hash, ok := blockNrOrHash.Hash(); ok {
			header = chain.GetHeaderByHash(hash)
		} else if number, ok := blockNrOrHash.Number(); ok {
			header = chain.GetHeaderByNumber(uint64(number.Int64()))
		}
		if header == nil {
			return nil, fmt.Errorf("missing block %v", blockNrOrHash.String())
		}
		return header, nil
	}
	block := new(types.Block)
	if err := rlp.DecodeBytes(rlpOrBlockNr.RLP, block); err == nil {
		return block.Header(), nil
	}
	header := new(types.Header)
	if err := rlp.DecodeBytes(rlpOrBlockNr.RLP, header); err != nil {
		return nil, err
	}
	return header, nil
}
