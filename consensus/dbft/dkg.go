package dbft

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/exp/slices"
)

type TxWatchRetry struct {
	SendHeight       uint64
	EndHeight        uint64
	TxHash           *common.Hash
	Method           string
	Params           []interface{}
	ConfirmedSuccess bool
}

// handleDKG handles the transaction submission for DKG process.
// It constructs and sends transaction to KeyManagement contract using amev store.
func (c *DBFT) handleDKG(h *types.Header) error {
	currentHeight := h.Number.Uint64()

	// If the current height exceeds the target height, then get the new target height
	if c.targetHeight < currentHeight {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}

		currentEpochStartHeight, err := c.currentEpochStartHeight(state, h)
		if err != nil {
			return fmt.Errorf("failed to call currentEpochStartHeight: %w", err)
		}
		epochDuration, err := c.epochDuration(state, h)
		if err != nil {
			return fmt.Errorf("failed to call epochDuration: %w", err)
		}
		sharePeriodDuration, err := c.sharePeriodDuration(state, h)
		if err != nil {
			return fmt.Errorf("failed to call currentEpochStartHeight: %w", err)
		}
		consensusList, err := c.getCurrentConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getCurrentConsensus: %w", err)
		}
		c.round, err = c.roundNumber(state, h)
		if err != nil {
			return fmt.Errorf("failed to call roundNumber: %w", err)
		}
		c.epochStartHeight = currentEpochStartHeight
		c.targetHeight = currentEpochStartHeight + epochDuration
		c.shareDuration = sharePeriodDuration
		c.consensusList = consensusList

		log.Info("DKG info", "roundNumber", c.round, "currentEpochStartHeight", currentEpochStartHeight, "epochDuration", epochDuration,
			"sharePeriodDuration", sharePeriodDuration, "consensusList", consensusList)
	}

	shareStartHeight := c.targetHeight - 2*c.shareDuration
	recoverStartHeight := shareStartHeight + c.shareDuration
	consensusSize := uint64(len(c.consensusList))
	amevAddress := c.amevKeystore.Address()

	// Retry transaction sending if watch list is not empty
	var retryList []*TxWatchRetry
	if currentHeight > shareStartHeight+1 && currentHeight < c.targetHeight {
		if len(c.txWatchList) > 0 {
			for _, item := range c.txWatchList {
				if currentHeight < item.EndHeight && !item.ConfirmedSuccess {
					needRetry := false
					// send failed, just resend and set txHash
					if item.TxHash == nil {
						needRetry = true
					}

					// send successfully, wait 3 blocks to check tx status
					if item.TxHash != nil && currentHeight-item.SendHeight == 3 {
						receipt, err := c.txAPI.GetTransactionReceipt(context.Background(), *item.TxHash)
						if err != nil {
							needRetry = true
							log.Error("DKG get transaction receipt failed", "err", err, "txHash", item.TxHash)
						}
						if receipt["status"] != uint(1) {
							needRetry = true
							log.Error("DKG get transaction receipt status error", "txHash", item.TxHash, "status", receipt["status"])
						}
					}

					var err error
					if needRetry {
						item.TxHash, err = c.sendTxToKeyManagement(item.Method, item.Params...)
						if err != nil {
							retryList = append(retryList, item)
							log.Error("retry sending transaction failed", "currentHeight", currentHeight, "method", item.Method, "err", err)
							continue
						} else {
							item.SendHeight = currentHeight
							retryList = append(retryList, item)
							log.Info("DKG retry transaction sent", "method", item.Method, "txHash", item.TxHash)
						}
					} else {
						item.ConfirmedSuccess = true
						log.Info("DKG get transaction receipt successfully", "method", item.Method, "txHash", item.TxHash)
					}
				}
			}
			// Only keep retry failed and not reach max retry times
			c.txWatchList = retryList
		}
	}

	// Send share and reshare tx when currentHeight == shareStartHeight+1
	if currentHeight == shareStartHeight+1 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		pendingConsensusList, err := c.getPendingConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getPendingConsensus: %w", err)
		}

		isCurrentConsensus := slices.Contains(c.consensusList, amevAddress)
		isPendingConsensus := slices.Contains(pendingConsensusList, amevAddress)

		// OnValidatorList starts a DKG
		var pubs []*ecies.PublicKey
		for _, addr := range pendingConsensusList {
			pubKey, err := c.messagePubkeys(&addr, state, h)
			if err != nil {
				return fmt.Errorf("failed to call messagePubkeys: %w", err)
			}
			pubs = append(pubs, pubKey)
		}
		err = c.amevKeystore.OnValidatorList(pendingConsensusList, pubs)
		if err != nil {
			return fmt.Errorf("failed to call amevKeystore.OnValidatorList, err: %w", err)
		}

		var shareErr, reshareErr error
		// No need reshare for round 0
		if isCurrentConsensus && c.round > 0 {
			rMsgs, rPvss, err := c.amevKeystore.DKGReshare()
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore.DKGReshare, err: %w", err)
			}
			// Send reshare tx
			txHash, err := c.reshare(rPvss, rMsgs)
			txWatch := &TxWatchRetry{SendHeight: currentHeight, EndHeight: recoverStartHeight, Method: "reshare", Params: []interface{}{rPvss, rMsgs}}
			if err != nil {
				c.txWatchList = append(c.txWatchList, txWatch)
				reshareErr = fmt.Errorf("failed to send reshare transaction, err: %w", err)
			} else {
				txWatch.TxHash = txHash
				c.txWatchList = append(c.txWatchList, txWatch)
				log.Info("DKG reshare transaction sent", "txHash", txHash)
			}
		}

		if isPendingConsensus {
			sMsgs, sPvss, err := c.amevKeystore.DKGShare()
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore.DKGShare, err: %w", err)
			}
			// Send share tx
			txHash, err := c.share(sPvss, sMsgs)
			txWatch := &TxWatchRetry{SendHeight: currentHeight, EndHeight: recoverStartHeight, Method: "share", Params: []interface{}{sPvss, sMsgs}}
			if err != nil {
				c.txWatchList = append(c.txWatchList, txWatch)
				shareErr = fmt.Errorf("failed to send share transaction, err: %w", err)
			} else {
				txWatch.TxHash = txHash
				c.txWatchList = append(c.txWatchList, txWatch)
				log.Info("DKG share transaction sent", "txHash", txHash)
			}
		}
		if reshareErr != nil {
			return reshareErr
		}
		if shareErr != nil {
			return shareErr
		}
	}

	// Check isShareReady at height recoverStartHeight+1
	if currentHeight == recoverStartHeight+1 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		ready, err := c.isShareReady(state, h)
		if err != nil {
			return fmt.Errorf("failed to call isShareReady: %w", err)
		}
		if !ready {
			return fmt.Errorf("DKG failed, share msgs not enough")
		}

		pendingConsensusList, err := c.getPendingConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getPendingConsensus: %w", err)
		}

		// If share is ready, pending consensus nodes should ReceiveSecretShare
		if slices.Contains(pendingConsensusList, amevAddress) {
			for i := uint64(1); i <= uint64(consensusSize); i++ {
				// Call ReceiveSecretShare
				shareMsgs, err := c.getShareMsgs(c.round, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call shareMsgs: %w", err)
				}
				spvss, err := c.spvsses(c.round, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call spvsses: %w", err)
				}
				err = c.amevKeystore.ReceiveSecretShare(pendingConsensusList[i-1], shareMsgs, spvss)
				if err != nil {
					return fmt.Errorf("failed to call amevKeystore.ReceiveSecretShare(), err: %w", err)
				}
				// Call ReceiveSecretReshare
				rpvss, err := c.rpvsses(c.round, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call rpvsses: %w", err)
				}
				// Only receive reshare has value
				if len(rpvss) > 0 {
					reshareMsgs, err := c.getReshareMsgs(c.round, i, state, h)
					if err != nil {
						return fmt.Errorf("failed to call reshareMsgs: %w", err)
					}
					err = c.amevKeystore.ReceiveSecretReshare(c.consensusList[i-1], reshareMsgs, rpvss)
					if err != nil {
						return fmt.Errorf("failed to call amevKeystore.ReceiveSecretReshare, err: %w", err)
					}
				}
			}
		}

		indexesNeedRecover, err := c.indexCurrentNeedRecovering(state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexCurrentNeedRecovering, err: %w", err)
		}
		if len(indexesNeedRecover) == 0 {
			// Reshare finished, no need to recover
			return nil
		}

		// only indexesNeedRecover <= (consensusSize - threshold) can recover
		threshold := consensusSize - (consensusSize-1)/3
		if len(indexesNeedRecover) > int(consensusSize-threshold) {
			return fmt.Errorf("reshare msgs not enough, cannot do recover")
		}

		// OnRecoverPeriodStart
		var recoverAddrs []common.Address
		var recoverPubKeys []*ecies.PublicKey
		var indexes []int
		for _, index := range indexesNeedRecover {
			indexes = append(indexes, int(index))
			recoverAddrs = append(recoverAddrs, pendingConsensusList[index-1])
			pubKey, err := c.messagePubkeys(&pendingConsensusList[index-1], state, h)
			if err != nil {
				return fmt.Errorf("failed to call messagePubkeys: %w", err)
			}
			recoverPubKeys = append(recoverPubKeys, pubKey)
		}
		err = c.amevKeystore.OnRecoverPeriodStart(indexes, recoverAddrs, recoverPubKeys)
		if err != nil {
			return fmt.Errorf("failed to call amevKeystore OnRecoverPeriodStart: %w", err)
		}
		// Send recover tx from current consensus node
		if slices.Contains(c.consensusList, amevAddress) {
			msgs, err := c.amevKeystore.DKGRecover()
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore DKGRecover: %w", err)
			}

			// Send recover tx
			txHash, err := c.recover(indexesNeedRecover, msgs)
			txWatch := &TxWatchRetry{SendHeight: currentHeight, EndHeight: recoverStartHeight + 1 + c.shareDuration/2, Method: "recover", Params: []interface{}{indexesNeedRecover, msgs}}
			if err != nil {
				c.txWatchList = append(c.txWatchList, txWatch)
				return fmt.Errorf("failed to send recover transaction: %w", err)
			}
			txWatch.TxHash = txHash
			c.txWatchList = append(c.txWatchList, txWatch)
			log.Info("DKG recover transaction sent", "txHash", txHash)
		}
	}

	// Send reshareRecovered at height recoverStartHeight+1+c.shareDuration/2
	if c.amevKeystore.IsRecovering() && currentHeight == recoverStartHeight+1+c.shareDuration/2 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		indexesNeedRecover, err := c.indexCurrentNeedRecovering(state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexCurrentNeedRecovering, err: %w", err)
		}
		// Only index in indexsNeedRecover and pending consensus node need to call reshareRecovered
		indexOfSharing, err := c.indexOfSharing(&amevAddress, state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexOfSharing: %w", err)
		}
		if indexOfSharing > 0 {
			if slices.Contains(indexesNeedRecover, indexOfSharing) {
				for i := uint64(1); i <= consensusSize; i++ {
					if !slices.Contains(indexesNeedRecover, i) {
						msg, err := c.recoverMsgs(c.round, i, indexOfSharing, state, h)
						if err != nil {
							return fmt.Errorf("failed to call recoverMsgs: %w", err)
						}
						pvss, err := c.spvsses(c.epochStartHeight, i, state, h)
						if err != nil {
							return fmt.Errorf("failed to call spvsses: %w", err)
						}
						err = c.amevKeystore.ReceiveRecoverShare(c.consensusList[i-1], msg, pvss)
						if err != nil {
							return fmt.Errorf("failed to call ReceiveRecoverShare: %w", err)
						}
					}
				}
				// Recover the lost resharing messages
				msgs, pvss, err := c.amevKeystore.TryRecoverReshare()
				if err != nil {
					return fmt.Errorf("failed to call TryRecoverReshare: %w", err)
				}
				// Send reshareRecovered tx
				txHash, err := c.reshareRecovered(pvss, msgs)
				txWatch := &TxWatchRetry{SendHeight: currentHeight, EndHeight: c.targetHeight, Method: "reshareRecovered", Params: []interface{}{pvss, msgs}}
				if err != nil {
					c.txWatchList = append(c.txWatchList, txWatch)
					return fmt.Errorf("failed to send reshareRecovered transaction: %w", err)
				}
				txWatch.TxHash = txHash
				c.txWatchList = append(c.txWatchList, txWatch)
				log.Info("DKG reshareRecovered transaction sent", "txHash", txHash)
			}
		}
	}

	// Call ReceiveRecoveredReshare at targetHeight, only at this block height we can get aggregatedCommitments
	if currentHeight == c.targetHeight {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		if c.amevKeystore.IsRecovering() {
			indexesNeedRecover, err := c.indexCurrentNeedRecovering(state, h)
			if err != nil {
				return fmt.Errorf("failed to call indexCurrentNeedRecovering, err: %w", err)
			}
			pendingConsensus, err := c.getPendingConsensus(state, h)
			if err != nil {
				return fmt.Errorf("failed to call getPendingConsensus: %w", err)
			}
			for _, index := range indexesNeedRecover {
				// Call ReceiveSecretReshare
				rpvss, err := c.rpvsses(c.round, index, state, h)
				if err != nil {
					return fmt.Errorf("failed to call rpvsses: %w", err)
				}
				reshareMsgs, err := c.getReshareMsgs(c.round, index, state, h)
				if err != nil {
					return fmt.Errorf("failed to call reshareMsgs: %w", err)
				}
				err = c.amevKeystore.ReceiveRecoveredReshare(pendingConsensus[index-1], reshareMsgs, rpvss)
				if err != nil {
					return fmt.Errorf("failed to call amevKeystore.ReceiveSecretReshare, err: %w", err)
				}
			}
		}
		aggregatedCommitments, err := c.aggregatedCommitments(c.round, state, h)
		if err != nil {
			return fmt.Errorf("failed to call aggregatedCommitments, err: %w", err)
		}
		if len(aggregatedCommitments) > 0 && c.round > 0 {
			isRoundNumberIncreased, _ := c.isRoundNumberIncreased(c.targetHeight, c.epochStartHeight, state, h)
			if !isRoundNumberIncreased {
				aggregatedCommitments = make([]byte, 0)
			}
		}
		err = c.amevKeystore.OnEpochChange(aggregatedCommitments)
		if err != nil {
			return fmt.Errorf("failed to call amevKeystore.OnEpochChange, err: %w", err)
		}
		log.Info("DKG reached targetHeight", "c.round", c.round, "currentHeight", currentHeight, "aggregatedCommitments", hex.EncodeToString(aggregatedCommitments))
	}

	return nil
}

func (c *DBFT) getCurrentConsensus(state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "getCurrentConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) getPendingConsensus(state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "getPendingConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) sharePeriodDuration(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "sharePeriodDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) epochDuration(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "epochDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) currentEpochStartHeight(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "currentEpochStartHeight")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) messagePubkeys(addr *common.Address, state *state.StateDB, header *types.Header) (*ecies.PublicKey, error) {
	var result string
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "messagePubkeys", addr)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		err = errors.New("messagePubkey is empty, addr: " + addr.String())
		return nil, err
	}
	keyBytes, err := hex.DecodeString(result)
	if err != nil {
		return nil, err
	}
	key, err := crypto.UnmarshalPubkey(keyBytes)
	if err != nil {
		return nil, err
	}
	return ecies.ImportECDSAPublic(key), nil
}

func (c *DBFT) indexOfSharing(addr *common.Address, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "indexOfSharing", addr)
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) indexOfResharing(addr *common.Address, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "indexOfResharing", addr)
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) indexCurrentNeedRecovering(state *state.StateDB, header *types.Header) ([]uint64, error) {
	var result []*big.Int
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "indexCurrentNeedRecovering")
	if err != nil {
		return nil, err
	}

	var indexs []uint64
	for _, item := range result {
		indexs = append(indexs, item.Uint64())
	}
	return indexs, nil
}

func (c *DBFT) isShareReady(state *state.StateDB, header *types.Header) (bool, error) {
	var result bool
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "isShareReady")
	if err != nil {
		return false, err
	}
	return result, nil
}

func (c *DBFT) getReshareMsgs(round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getReshareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) rpvsses(round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "rpvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) getShareMsgs(round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getShareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) spvsses(round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "spvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) recoverMsgs(round, indexSend, indexReceive uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "recoverMsgs", big.NewInt(int64(round)), big.NewInt(int64(indexSend)), big.NewInt(int64(indexReceive)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) aggregatedCommitments(round uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "aggregatedCommitments", big.NewInt(int64(round)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) roundNumber(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "roundNumber")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func (c *DBFT) isRoundNumberIncreased(epochHeight, lastEpochHeight uint64, state *state.StateDB, header *types.Header) (bool, error) {
	var result bool
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "isRoundNumberIncreased", big.NewInt(int64(epochHeight)), big.NewInt(int64(lastEpochHeight)))
	if err != nil {
		return false, err
	}
	return result, nil
}

func (c *DBFT) readContract(res interface{}, contract common.Address, contractAbi abi.ABI,
	state *state.StateDB, header *types.Header,
	method string, args ...interface{}) error {
	data, err := contractAbi.Pack(method, args...)
	if err != nil {
		return fmt.Errorf("failed to pack '%s': %w", method, err)
	}
	msgData := hexutil.Bytes(data)
	gas := hexutil.Uint64(50_000_000) // More than enough for validators call processing.
	txArgs := ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &contract,
		Data: &msgData,
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel when we are finished consuming integers.
	defer cancel()
	result, err := c.ethAPI.CallAtState(ctx, txArgs, state, header)
	if err != nil {
		return fmt.Errorf("failed to call at state '%s': %w", method, err)
	}
	results, err := contractAbi.Unpack(method, result)
	if err != nil {
		return fmt.Errorf("failed to unpack result: %w", err)
	}
	res = abi.ConvertType(results[0], res)
	return nil
}

func (c *DBFT) reshare(pvss []byte, messages [][]byte) (*common.Hash, error) {
	return c.sendTxToKeyManagement("reshare", pvss, messages)
}

func (c *DBFT) share(pvss []byte, messages [][]byte) (*common.Hash, error) {
	return c.sendTxToKeyManagement("share", pvss, messages)
}

func (c *DBFT) reshareRecovered(pvss []byte, messages [][]byte) (*common.Hash, error) {
	return c.sendTxToKeyManagement("reshareRecovered", pvss, messages)
}

func (c *DBFT) recover(idxs []uint64, messages [][]byte) (*common.Hash, error) {
	var idxsBigInt []*big.Int
	for _, idx := range idxs {
		idxsBigInt = append(idxsBigInt, big.NewInt(int64(idx)))
	}
	return c.sendTxToKeyManagement("recover", idxsBigInt, messages)
}

func (c *DBFT) registerMessageKey(candidate common.Address, pubkey string) (*common.Hash, error) {
	return c.sendTxToKeyManagement("registerMessageKey", &candidate, pubkey)
}

func (c *DBFT) sendTxToKeyManagement(method string, args ...interface{}) (*common.Hash, error) {
	if c.txAPI == nil {
		return nil, errors.New("eth transaction API is not initialized, dBFT can't function properly")
	}
	data, err := systemcontracts.KeyManagementABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to pack '%s': %w", method, err)
	}
	msgData := hexutil.Bytes(data)

	txHash, err := c.txAPI.SendTransaction(context.Background(),
		ethapi.TransactionArgs{
			From: &c.signer,
			To:   &systemcontracts.KeyManagementProxyHash,
			Data: &msgData})

	if err != nil {
		return nil, fmt.Errorf("failed to send tx with consensus node, to %s data: '%s': %w", systemcontracts.KeyManagementProxyHash, hex.EncodeToString(data), err)
	}
	return &txHash, nil
}
