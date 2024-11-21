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
	if currentHeight > c.targetHeight {
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
	if currentHeight > shareStartHeight && currentHeight < c.targetHeight {
		if len(c.txWatchList) > 0 {
			for _, item := range c.txWatchList {
				if currentHeight < item.EndHeight && !item.ConfirmedSuccess {
					needRetry := false
					// Send failed, just resend and set txHash
					if item.TxHash == nil {
						needRetry = true
					}

					// Send successfully, wait 3 blocks to check tx status
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

	// If keystore is empty, then sync shared DKG up-tp-date
	if !c.amevKeystore.HasShared() && !c.amevKeystore.IsSharing() {
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		// Sync only if has at least 1 round successful DKG
		if c.round > 0 {
			// Use current consensus to setup
			if err = c.prepareDKG(c.consensusList, state, h); err != nil {
				return fmt.Errorf("failed to sync shared DKG, err: %w", err)
			}
			aggregatedCommitments, err := c.aggregatedCommitments(c.round-1, state, h)
			if err != nil {
				return fmt.Errorf("failed to call aggregatedCommitments, err: %w", err)
			}
			if err := c.amevKeystore.OnEpochChange(aggregatedCommitments); err != nil {
				return fmt.Errorf("failed to sync shared DKG, err: %w", err)
			}
			log.Info("DKG sync to", "round", c.round-1, "currentHeight", currentHeight, "aggregatedCommitments", hex.EncodeToString(aggregatedCommitments))
		}
		// Then sync the sharing one if there is, but don't send transactions
		if currentHeight > shareStartHeight {
			pendingConsensusList, err := c.getPendingConsensus(state, h)
			if err != nil {
				return fmt.Errorf("failed to call getPendingConsensus: %w", err)
			}
			// Use pending consensus to setup
			if err = c.prepareDKG(pendingConsensusList, state, h); err != nil {
				return fmt.Errorf("failed to sync sharing DKG, err: %w", err)
			}
			if currentHeight > recoverStartHeight {
				ready, err := c.isShareReady(state, h)
				if err != nil {
					return fmt.Errorf("failed to call isShareReady: %w", err)
				}
				// If share is ready, pending consensus nodes should ReceiveSecretShare
				if ready && slices.Contains(pendingConsensusList, amevAddress) {
					if err := c.syncSharedSecrets(pendingConsensusList, state, h); err != nil {
						return fmt.Errorf("failed to sync sharing DKG, err: %w", err)
					}
					if err := c.syncResharedSecrets(c.consensusList, state, h); err != nil {
						return fmt.Errorf("failed to sync sharing DKG, err: %w", err)
					}
				}
			}
		}
	}

	// DKG checkpoint handling
	if currentHeight == shareStartHeight {
		// Send share and reshare tx when currentHeight == shareStartHeight
		state, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %w", err)
		}
		pendingConsensusList, err := c.getPendingConsensus(state, h)
		if err != nil {
			return fmt.Errorf("failed to call getPendingConsensus: %w", err)
		}
		// Prepare and start a DKG
		if err = c.prepareDKG(pendingConsensusList, state, h); err != nil {
			return fmt.Errorf("failed to start new DKG, err: %w", err)
		}
		var shareErr, reshareErr error
		// If is a member of current consensus
		if slices.Contains(c.consensusList, amevAddress) && c.round > 0 {
			reshareErr = c.taskReshare(currentHeight, recoverStartHeight)
		}
		// If is a member of pending consensus
		if slices.Contains(pendingConsensusList, amevAddress) {
			shareErr = c.taskShare(currentHeight, recoverStartHeight)
		}
		if reshareErr != nil {
			return reshareErr
		}
		if shareErr != nil {
			return shareErr
		}
	} else if currentHeight == recoverStartHeight {
		// Check isShareReady at height recoverStartHeight
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
			if err := c.syncSharedSecrets(pendingConsensusList, state, h); err != nil {
				return fmt.Errorf("failed to sync sharing DKG, err: %w", err)
			}
			if err := c.syncResharedSecrets(c.consensusList, state, h); err != nil {
				return fmt.Errorf("failed to sync resharing DKG, err: %w", err)
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

		// Only indexesNeedRecover <= (consensusSize - threshold) can recover
		threshold := consensusSize - (consensusSize-1)/3
		if len(indexesNeedRecover) > int(consensusSize-threshold) {
			return fmt.Errorf("reshare msgs not enough, cannot do recover")
		}

		if err := c.prepareRecover(pendingConsensusList, indexesNeedRecover, state, h); err != nil {
			return fmt.Errorf("failed to start DKG recover, err: %w", err)
		}
		// Send recover tx from current consensus node
		if slices.Contains(c.consensusList, amevAddress) {
			if err := c.taskRecover(indexesNeedRecover, currentHeight, recoverStartHeight+c.shareDuration/2); err != nil {
				return err
			}
		}
	} else if c.amevKeystore.IsRecovering() && currentHeight == recoverStartHeight+c.shareDuration/2 {
		// Send reshareRecovered at height recoverStartHeigh+c.shareDuration/2
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
				if err := c.syncRecoveredSecrets(indexesNeedRecover, indexOfSharing, state, h); err != nil {
					return fmt.Errorf("failed to sync recovering DKG, err: %w", err)
				}
				if err := c.taskReshareRecover(currentHeight, c.targetHeight); err != nil {
					return err
				}
			}
		}
	} else if currentHeight == c.targetHeight {
		// Call ReceiveRecoveredReshare at targetHeight, only at this block height we can get aggregatedCommitments
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
			if err := c.syncRecoveredReshares(pendingConsensus, indexesNeedRecover, state, h); err != nil {
				return fmt.Errorf("failed to sync recovering DKG, err: %w", err)
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

// prepareDKG collects DKG participants' message keys and sends to keystore
func (c *DBFT) prepareDKG(participants []common.Address, state *state.StateDB, header *types.Header) error {
	var pubs []*ecies.PublicKey
	for _, addr := range participants {
		pubKey, err := c.messagePubkeys(&addr, state, header)
		if err != nil {
			return fmt.Errorf("failed to call messagePubkeys: %w", err)
		}
		pubs = append(pubs, pubKey)
	}
	return c.amevKeystore.OnValidatorList(participants, pubs)
}

// prepareRecover collects recover participants' message keys and sends to keystore
func (c *DBFT) prepareRecover(participants []common.Address, indexesNeedRecover []uint64, state *state.StateDB, header *types.Header) error {
	// OnRecoverPeriodStart
	var recoverAddrs []common.Address
	var recoverPubKeys []*ecies.PublicKey
	var indexes []int
	for _, index := range indexesNeedRecover {
		indexes = append(indexes, int(index))
		recoverAddrs = append(recoverAddrs, participants[index-1])
		pubKey, err := c.messagePubkeys(&participants[index-1], state, header)
		if err != nil {
			return fmt.Errorf("failed to call messagePubkeys: %w", err)
		}
		recoverPubKeys = append(recoverPubKeys, pubKey)
	}
	return c.amevKeystore.OnRecoverPeriodStart(indexes, recoverAddrs, recoverPubKeys)
}

// syncSharedSecrets downloads DKG sharings and related PVSS, and sends to keystore
func (c *DBFT) syncSharedSecrets(participants []common.Address, state *state.StateDB, header *types.Header) error {
	for i := uint64(1); i <= uint64(len(participants)); i++ {
		// Call ReceiveSecretShare
		shareMsgs, err := c.getShareMsgs(c.round, i, state, header)
		if err != nil {
			return fmt.Errorf("failed to call shareMsgs: %w", err)
		}
		spvss, err := c.spvsses(c.round, i, state, header)
		if err != nil {
			return fmt.Errorf("failed to call spvsses: %w", err)
		}
		err = c.amevKeystore.ReceiveSecretShare(participants[i-1], shareMsgs, spvss)
		if err != nil {
			return err
		}
	}
	return nil
}

// syncResharedSecrets downloads DKG resharings and related PVSS, and sends to keystore
func (c *DBFT) syncResharedSecrets(participants []common.Address, state *state.StateDB, header *types.Header) error {
	for i := uint64(1); i <= uint64(len(participants)); i++ {
		// Call ReceiveSecretReshare
		rpvss, err := c.rpvsses(c.round, i, state, header)
		if err != nil {
			return fmt.Errorf("failed to call rpvsses: %w", err)
		}
		// Only receive reshare has value
		if len(rpvss) > 0 {
			reshareMsgs, err := c.getReshareMsgs(c.round, i, state, header)
			if err != nil {
				return fmt.Errorf("failed to call reshareMsgs: %w", err)
			}
			err = c.amevKeystore.ReceiveSecretReshare(participants[i-1], reshareMsgs, rpvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncRecoveredSecrets downloads DKG recoverings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredSecrets(indexesNeedRecover []uint64, selfIndex uint64, state *state.StateDB, header *types.Header) error {
	for i := uint64(1); i <= uint64(len(c.consensusList)); i++ {
		if !slices.Contains(indexesNeedRecover, i) {
			msg, err := c.recoverMsgs(c.round, i, selfIndex, state, header)
			if err != nil {
				return fmt.Errorf("failed to call recoverMsgs: %w", err)
			}
			pvss, err := c.spvsses(c.epochStartHeight, i, state, header)
			if err != nil {
				return fmt.Errorf("failed to call spvsses: %w", err)
			}
			err = c.amevKeystore.ReceiveRecoverShare(c.consensusList[i-1], msg, pvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncRecoveredReshares downloads recovered DKG resharings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredReshares(participants []common.Address, indexesNeedRecover []uint64, state *state.StateDB, header *types.Header) error {
	for _, index := range indexesNeedRecover {
		// Call ReceiveSecretReshare
		rpvss, err := c.rpvsses(c.round, index, state, header)
		if err != nil {
			return fmt.Errorf("failed to call rpvsses: %w", err)
		}
		reshareMsgs, err := c.getReshareMsgs(c.round, index, state, header)
		if err != nil {
			return fmt.Errorf("failed to call reshareMsgs: %w", err)
		}
		err = c.amevKeystore.ReceiveRecoveredReshare(participants[index-1], reshareMsgs, rpvss)
		if err != nil {
			return err
		}
	}
	return nil
}

// taskShare tries to send secret shares as a transaction
func (c *DBFT) taskShare(start uint64, end uint64) error {
	sMsgs, sPvss, err := c.amevKeystore.DKGShare()
	if err != nil {
		return err
	}
	// Send share tx
	txHash, err := c.share(sPvss, sMsgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "share", Params: []interface{}{sPvss, sMsgs}}
	if err != nil {
		c.txWatchList = append(c.txWatchList, txWatch)
		return fmt.Errorf("failed to send share transaction, err: %w", err)
	} else {
		txWatch.TxHash = txHash
		c.txWatchList = append(c.txWatchList, txWatch)
		log.Info("DKG share transaction sent", "txHash", txHash)
		return nil
	}
}

// taskReshare tries to send secret reshares as a transaction
func (c *DBFT) taskReshare(start uint64, end uint64) error {
	rMsgs, rPvss, err := c.amevKeystore.DKGReshare()
	if err != nil {
		return err
	}
	// Send reshare tx
	txHash, err := c.reshare(rPvss, rMsgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "reshare", Params: []interface{}{rPvss, rMsgs}}
	if err != nil {
		c.txWatchList = append(c.txWatchList, txWatch)
		return fmt.Errorf("failed to send reshare transaction, err: %w", err)
	} else {
		txWatch.TxHash = txHash
		c.txWatchList = append(c.txWatchList, txWatch)
		log.Info("DKG reshare transaction sent", "txHash", txHash)
		return nil
	}
}

// taskRecover tries to send past secret shares as a transaction
func (c *DBFT) taskRecover(indexesNeedRecover []uint64, start uint64, end uint64) error {
	msgs, err := c.amevKeystore.DKGRecover()
	if err != nil {
		return err
	}
	// Send recover tx
	txHash, err := c.recover(indexesNeedRecover, msgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "recover", Params: []interface{}{indexesNeedRecover, msgs}}
	if err != nil {
		c.txWatchList = append(c.txWatchList, txWatch)
		return fmt.Errorf("failed to send recover transaction: %w", err)
	} else {
		txWatch.TxHash = txHash
		c.txWatchList = append(c.txWatchList, txWatch)
		log.Info("DKG recover transaction sent", "txHash", txHash)
		return nil
	}
}

// taskReshareRecover tries to send recovered secret reshares as a transaction
func (c *DBFT) taskReshareRecover(start uint64, end uint64) error {
	// Recover the lost resharing messages
	msgs, pvss, err := c.amevKeystore.TryRecoverReshare()
	if err != nil {
		return fmt.Errorf("failed to call TryRecoverReshare: %w", err)
	}
	// Send reshareRecovered tx
	txHash, err := c.reshareRecovered(pvss, msgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "reshareRecovered", Params: []interface{}{pvss, msgs}}
	if err != nil {
		c.txWatchList = append(c.txWatchList, txWatch)
		return fmt.Errorf("failed to send reshareRecovered transaction: %w", err)
	} else {
		txWatch.TxHash = txHash
		c.txWatchList = append(c.txWatchList, txWatch)
		log.Info("DKG reshareRecovered transaction sent", "txHash", txHash)
		return nil
	}
}

// getCurrentConsensus returns an address list of current CNs
func (c *DBFT) getCurrentConsensus(state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "getCurrentConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getPendingConsensus returns an address list of pending CNs
func (c *DBFT) getPendingConsensus(state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "getPendingConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// sharePeriodDuration returns a number of blocks as the duration of each sharing period
func (c *DBFT) sharePeriodDuration(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "sharePeriodDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// sharePeriodDuration returns a number of blocks as the duration of each governanace epoch
func (c *DBFT) epochDuration(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "epochDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// currentEpochStartHeight returns the block height when the current governanace epoch starts
func (c *DBFT) currentEpochStartHeight(state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI,
		state, header, "currentEpochStartHeight")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// messagePubkeys returns the message key of input address
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

// indexOfSharing returns the DKG index of input address
func (c *DBFT) indexOfSharing(addr *common.Address, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := c.readContract(&result, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "indexOfSharing", addr)
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// indexCurrentNeedRecovering returns an array of DKG index that needs recover
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

// isShareReady checks if the DKG sharing is 100% uploaded
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
