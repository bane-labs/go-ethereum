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

type TxSendRetry struct {
	SendHeight   uint64
	Method       string
	Params       []interface{}
	RetryTimes   int
	RetrySuccess bool
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
		c.targetHeight = currentEpochStartHeight + epochDuration
		c.preEpochStartHeight = currentEpochStartHeight - epochDuration
		c.shareDuration = sharePeriodDuration
		c.shareReady = false
		c.consensusList = consensusList
		c.needRecover = false

		log.Info("dkg info", "currentEpochStartHeight", currentEpochStartHeight, "epochDuration", epochDuration,
			"sharePeriodDuration", sharePeriodDuration, "consensusList", consensusList)
	}

	shareStartHeight := c.targetHeight - 2*c.shareDuration
	recoverStartHeight := shareStartHeight + c.shareDuration
	consensusSize := uint64(len(c.consensusList))
	amevAddress := c.amevKeystore.Address()

	// send registerMessageKey at shareStartHeight-2, only for testing now
	if currentHeight == shareStartHeight-2 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		_, err = c.messagePubkeys(&amevAddress, state, h)
		if err != nil {
			// send registerMessageKey tx
			txHash, err := c.registerMessageKey(amevAddress, c.amevKeystore.MessagePubKey())
			if err != nil {
				return fmt.Errorf("failed to send registerMessageKey transaction, err: %w", err)
			}
			log.Info("registerMessageKey transaction sent", "txHash", txHash)
		}
	}

	// send share and reshare tx when currentHeight == shareStartHeight+1
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
		if isCurrentConsensus {
			rMsgs, rPvss, err := c.amevKeystore.DKGReshare()
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore.DKGReshare, err: %w", err)
			}
			// send reshare tx
			txHash, err := c.reshare(rPvss, rMsgs)
			if err != nil {
				c.txRetryList = append(c.txRetryList, &TxSendRetry{SendHeight: currentHeight, Method: "reshare", Params: []interface{}{rPvss, rMsgs}})
				reshareErr = fmt.Errorf("failed to send reshare transaction, err: %w", err)
			} else {
				log.Info("reshare transaction sent", "txHash", txHash)
			}
		}

		if isPendingConsensus {
			sMsgs, sPvss, err := c.amevKeystore.DKGShare()
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore.DKGShare, err: %w", err)
			}
			// send share tx
			txHash, err := c.share(sPvss, sMsgs)
			if err != nil {
				c.txRetryList = append(c.txRetryList, &TxSendRetry{SendHeight: currentHeight, Method: "share", Params: []interface{}{sPvss, sMsgs}})
				shareErr = fmt.Errorf("failed to send share transaction, err: %w", err)
			} else {
				log.Info("share transaction sent", "txHash", txHash)
			}
		}
		if reshareErr != nil {
			return reshareErr
		}
		if shareErr != nil {
			return shareErr
		}
	}

	// retry transaction sending if retry list is not empty
	var retryList []*TxSendRetry
	if currentHeight > shareStartHeight+1 && currentHeight < c.targetHeight {
		if len(c.txRetryList) > 0 {
			for _, item := range c.txRetryList {
				if item.RetryTimes < 3 && !item.RetrySuccess {
					txHash, err := c.sendTxToKeyManagement(item.Method, item.Params...)
					if err != nil {
						item.RetryTimes++
						retryList = append(retryList, item)
						log.Error("retry sending transaction failed", "currentHeight", currentHeight, "method", item.Method, "err", err)
						continue
					}
					item.RetrySuccess = true
					log.Info("retry sending transaction successfully", "method", item.Method, "txHash", txHash)
				}
			}
			// only keep retry failed and not reach max retry times
			c.txRetryList = retryList
		}
	}

	// check isShareReady at height recoverStartHeight+1
	if currentHeight == recoverStartHeight+1 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		c.shareReady, err = c.isShareReady(state, h)
		if err != nil {
			return fmt.Errorf("failed to call isShareReady: %w", err)
		}
		if !c.shareReady {
			return fmt.Errorf("DKG failed, share msgs not enough")
		}

		pendingConsensusList, err := c.getPendingConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getPendingConsensus: %w", err)
		}

		// if shareReady, pending consensus nodes should ReceiveSecretShare
		if slices.Contains(pendingConsensusList, amevAddress) {
			for i := uint64(1); i <= uint64(consensusSize); i++ {
				// call ReceiveSecretShare
				shareMsgs, err := c.getShareMsgs(c.targetHeight, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call shareMsgs: %w", err)
				}
				spvss, err := c.spvsses(c.targetHeight, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call spvsses: %w", err)
				}
				err = c.amevKeystore.ReceiveSecretShare(pendingConsensusList[i-1], shareMsgs, spvss)
				if err != nil {
					return fmt.Errorf("failed to call amevKeystore.ReceiveSecretShare(), err: %w", err)
				}
				// call ReceiveSecretReshare
				rpvss, err := c.rpvsses(c.targetHeight, i, state, h)
				if err != nil {
					return fmt.Errorf("failed to call rpvsses: %w", err)
				}
				// only receive reshare has value
				if len(rpvss) > 0 {
					reshareMsgs, err := c.getReshareMsgs(c.targetHeight, i, state, h)
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
		// reshare finished, no need to recover
		if len(indexesNeedRecover) == 0 {
			return nil
		}

		// only indexesNeedRecover <= (consensusSize - threshold) can recover
		threshold := consensusSize - (consensusSize-1)/3
		if len(indexesNeedRecover) > 0 && len(indexesNeedRecover) <= int(consensusSize-threshold) {
			c.needRecover = true
		}
		if !c.needRecover {
			return fmt.Errorf("reshare msgs not enough")
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
		// send recover tx from current consensus node
		if slices.Contains(c.consensusList, amevAddress) {
			msgs, err := c.amevKeystore.DKGRecover(indexes, recoverPubKeys)
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore DKGRecover: %w", err)
			}

			// send recover tx
			txHash, err := c.recover(indexesNeedRecover, msgs)
			if err != nil {
				c.txRetryList = append(c.txRetryList, &TxSendRetry{SendHeight: currentHeight, Method: "recover", Params: []interface{}{indexesNeedRecover, msgs}})
				return fmt.Errorf("failed to send recover transaction: %w", err)
			}
			log.Info("recover transaction sent", "txHash", txHash)
		}
	}

	// if share not ready, no need to recover
	if !c.shareReady {
		return nil
	}

	// send reshareRecovered at height recoverStartHeight+1+c.shareDuration/3
	if c.needRecover && currentHeight == recoverStartHeight+1+c.shareDuration/3 {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		indexesNeedRecover, err := c.indexCurrentNeedRecovering(state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexCurrentNeedRecovering, err: %w", err)
		}
		// only index in indexsNeedRecover and pending consensus node need to call reshareRecovered
		indexOfSharing, err := c.indexOfSharing(&amevAddress, state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexOfSharing: %w", err)
		}
		if indexOfSharing > 0 {
			if slices.Contains(indexesNeedRecover, indexOfSharing) {
				for i := uint64(1); i <= consensusSize; i++ {
					if !slices.Contains(indexesNeedRecover, i) {
						msg, err := c.recoverMsgs(c.targetHeight, i, indexOfSharing, state, h)
						if err != nil {
							return fmt.Errorf("failed to call recoverMsgs: %w", err)
						}
						pvss, err := c.spvsses(c.preEpochStartHeight, i, state, h)
						if err != nil {
							return fmt.Errorf("failed to call spvsses: %w", err)
						}
						err = c.amevKeystore.ReceiveRecoverShare(c.consensusList[i-1], msg, pvss)
						if err != nil {
							return fmt.Errorf("failed to call ReceiveRecoverShare: %w", err)
						}
					}
				}
				// recover the lost resharing messages
				msgs, pvss, err := c.amevKeystore.TryRecoverReshare()
				if err != nil {
					return fmt.Errorf("failed to call TryRecoverReshare: %w", err)
				}
				// send reshareRecovered tx
				txHash, err := c.reshareRecovered(pvss, msgs)
				if err != nil {
					c.txRetryList = append(c.txRetryList, &TxSendRetry{SendHeight: currentHeight, Method: "reshareRecovered", Params: []interface{}{pvss, msgs}})
					return fmt.Errorf("failed to send reshareRecovered transaction: %w", err)
				}
				log.Info("reshareRecovered transaction sent", "txHash", txHash)
			}

		}
	}

	// call ReceiveRecoveredReshare at targetHeight, only at this block height we can get aggregatedCommitments
	if c.needRecover && currentHeight == c.targetHeight {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		indexesNeedRecover, err := c.indexCurrentNeedRecovering(state, h)
		if err != nil {
			return fmt.Errorf("failed to call indexCurrentNeedRecovering, err: %w", err)
		}
		pendingConsensus, err := c.getPendingConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getPendingConsensus: %w", err)
		}
		for _, index := range indexesNeedRecover {
			// call ReceiveSecretReshare
			rpvss, err := c.rpvsses(c.targetHeight, index, state, h)
			if err != nil {
				return fmt.Errorf("failed to call rpvsses: %w", err)
			}
			reshareMsgs, err := c.getReshareMsgs(c.targetHeight, index, state, h)
			if err != nil {
				return fmt.Errorf("failed to call reshareMsgs: %w", err)
			}
			err = c.amevKeystore.ReceiveRecoveredReshare(pendingConsensus[index-1], reshareMsgs, rpvss)
			if err != nil {
				return fmt.Errorf("failed to call amevKeystore.ReceiveSecretReshare, err: %w", err)
			}
		}
		aggregatedCommitments, err := c.aggregatedCommitments(c.targetHeight, state, h)
		if err != nil {
			return fmt.Errorf("failed to call aggregatedCommitments, err: %w", err)
		}
		err = c.amevKeystore.OnEpochChange(aggregatedCommitments)
		if err != nil {
			return fmt.Errorf("failed to call amevKeystore.OnEpochChange, err: %w", err)
		}
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

func (c *DBFT) getReshareMsgs(height, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getReshareMsgs", big.NewInt(int64(height)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) rpvsses(height, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "rpvsses", big.NewInt(int64(height)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) getShareMsgs(height, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getShareMsgs", big.NewInt(int64(height)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) spvsses(height, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "spvsses", big.NewInt(int64(height)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) recoverMsgs(height, indexSend, indexReceive uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "recoverMsgs", big.NewInt(int64(height)), big.NewInt(int64(indexSend)), big.NewInt(int64(indexReceive)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *DBFT) aggregatedCommitments(height uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "aggregatedCommitments", big.NewInt(int64(height)))
	if err != nil {
		return nil, err
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
	gas := hexutil.Uint64(50_000_000) // more than enough for validators call processing.
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
