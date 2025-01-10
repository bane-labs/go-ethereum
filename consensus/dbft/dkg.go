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

// TxWatchRetry is a task to send DKG transaction
type TxWatchRetry struct {
	SendHeight       uint64
	EndHeight        uint64
	TxHash           *common.Hash
	Method           string
	Params           []interface{}
	ConfirmedSuccess bool
}

// TxWatchList is a watch task list send to loopTaskList by channel
type TxWatchList struct {
	WatchList     []TxWatchRetry
	CurrentHeight uint64
}

// Snapshot is a temporary record to save progress of a DKG round
type Snapshot struct {
	EpochStartHeight     uint64
	Round                uint64 // Starts from 1
	CurrentCNs           []common.Address
	PendingCNs           []common.Address
	IndexNeedRecover     []uint64
	ShareTasked          bool
	RecoverTasked        bool
	ReshareRecoverTasked bool
}

// newSnapshot creates a new snapshot with the specified startup parameters.
func (c *DBFT) newSnapshot(h *types.Header, state *state.StateDB, height uint64) (*Snapshot, error) {
	snap := &Snapshot{}
	snap.EpochStartHeight = height
	round, err := getRoundNumber(c.ethAPI, state, h)
	if err != nil {
		return nil, err
	}
	// Snapshot round index points to the new round, so plus 1
	snap.Round = round + 1
	snap.CurrentCNs, err = getCurrentConsensus(c.ethAPI, state, h)
	if err != nil {
		return nil, err
	}
	snap.PendingCNs = make([]common.Address, 0)
	snap.IndexNeedRecover = make([]uint64, 0)
	snap.ShareTasked = false
	snap.RecoverTasked = false
	snap.ReshareRecoverTasked = false
	return snap, nil
}

// handleDKG handles the transaction submission for DKG process.
// It constructs and sends transaction to KeyManagement contract using amev store.
func (c *DBFT) handleDKG(h *types.Header, state *state.StateDB) error {
	currentHeight := h.Number.Uint64()
	watchList := &TxWatchList{
		CurrentHeight: currentHeight,
		WatchList:     make([]TxWatchRetry, 0),
	}
	amevAddress := c.amevKeystore.Address()
	if state == nil {
		s, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %v", err)
		}
		state = s
	}
	epochDuration, err := getEpochDuration(c.ethAPI, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch epoch duration: %v", err)
	}
	sharePeriodDuration, err := getSharePeriodDuration(c.ethAPI, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch share period duration: %v", err)
	}

	// If there is an ongoing round and it's time to epoch change
	if c.dkgSnapshot != nil && currentHeight == c.dkgSnapshot.EpochStartHeight+epochDuration {
		indexOfSharing := slices.Index(c.dkgSnapshot.PendingCNs, amevAddress) + 1
		// Call ReceiveRecoveredReshare at targetHeight, only at this block height we can get aggregatedCommitments
		if len(c.dkgSnapshot.IndexNeedRecover) > 0 && indexOfSharing > 0 {
			if err := c.syncRecoveredReshares(c.dkgSnapshot, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync recovered secrets, err: %v", err)
			}
		}
		// if indexOfSharing is 0, then selfPvss should be nil
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113
		selfPvss, err := getSharePVSS(c.ethAPI, c.dkgSnapshot.Round, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvss, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.ethAPI, c.dkgSnapshot.Round, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		err = c.amevKeystore.OnEpochChange(selfPvss, aggregatedCommitment, indexOfSharing > 0)
		if err != nil {
			return fmt.Errorf("failed to change keystore epoch, err: %v", err)
		}
		log.Info("DKG reached targetHeight", "round", c.dkgSnapshot.Round, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		c.dkgSnapshot = nil
	}

	// If there is not a snapshot of current epoch, then new
	epochStartHeight, err := getCurrentEpochStartHeight(c.ethAPI, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch current epoch start height: %v", err)
	}
	if c.dkgSnapshot == nil {
		s, err := c.newSnapshot(h, state, epochStartHeight)
		if err != nil {
			return fmt.Errorf("failed to new DKG snapshot, err: %v", err)
		}
		c.dkgSnapshot = s
		log.Info("DKG info", "roundNumber", c.dkgSnapshot.Round, "epochStartHeight", c.dkgSnapshot.EpochStartHeight, "epochDuration", epochDuration,
			"sharePeriodDuration", sharePeriodDuration, "consensusList", c.dkgSnapshot.CurrentCNs)
	}
	// Compute periods based on realtime data, in case there is an update in governanace contract
	targetHeight := c.dkgSnapshot.EpochStartHeight + epochDuration
	shareStartHeight := targetHeight - 2*sharePeriodDuration
	recoverStartHeight := shareStartHeight + sharePeriodDuration
	recoverCheckHeight := recoverStartHeight + sharePeriodDuration/2
	consensusSize := uint64(len(c.dkgSnapshot.CurrentCNs))

	if currentHeight >= shareStartHeight && currentHeight < targetHeight {
		// Send watch task list to loopTaskChan when handleDKG finished
		defer func() {
			c.loopTaskChan <- watchList
		}()
	}

	// If keystore is out-of-date, then sync shared DKG up-tp-date
	keystoreRound := c.amevKeystore.Round()
	// If keystore has a round of future, then return an error
	if keystoreRound >= int(c.dkgSnapshot.Round) {
		return fmt.Errorf("invalid antimev keystore round index: %v", keystoreRound)
	}
	// If this round failed but keystore is still in a sharing state
	if keystoreRound == int(c.dkgSnapshot.Round)-1 && currentHeight < shareStartHeight && c.amevKeystore.IsSharing() {
		// Here only revert before the new share period, if the node wake up when share,
		// then data will be overwritten
		if err := c.amevKeystore.RevertRound(); err != nil {
			return err
		}
	}
	// Sync if keystore needs a sync and contract has at least 1 round successful DKG
	if keystoreRound < int(c.dkgSnapshot.Round)-1 {
		// If keystore is late for more than 1 round, than reset it to the last one,
		// past data are not helpful anymore
		if keystoreRound < int(c.dkgSnapshot.Round)-2 {
			if err := c.amevKeystore.Reset(int(c.dkgSnapshot.Round) - 2); err != nil {
				return err
			}
		}
		// If keystore is in a sharing state already, then regard it has a valid secret
		if !c.amevKeystore.IsSharing() {
			if err := c.amevKeystore.OnSharePeriodStart(); err != nil {
				return fmt.Errorf("failed to sync shared DKG, err: %v", err)
			}
		}
		// If is a member of current consensus, then try sync secrets
		indexOfSharing := slices.Index(c.dkgSnapshot.CurrentCNs, amevAddress) + 1
		if indexOfSharing > 0 {
			// Only a warning here, since it doesn't destroy dBFT and anti-mev,
			// a new DKG can perform and the next reshare is recoverable.
			// But it is dangerous if more than 1/3 CNs reinit their keystores
			// or change their message key.
			if err := c.syncLastRoundSecrets(c.dkgSnapshot, indexOfSharing, state, h); err != nil {
				log.Warn("failed to sync shared DKG", "err", err)
			}
		}
		// if indexOfSharing is 0, then selfPvss should be nil
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113
		selfPvss, err := getSharePVSS(c.ethAPI, c.dkgSnapshot.Round-1, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvsses, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.ethAPI, c.dkgSnapshot.Round-1, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		if err := c.amevKeystore.OnEpochChange(selfPvss, aggregatedCommitment, indexOfSharing > 0); err != nil {
			return fmt.Errorf("failed to sync shared DKG, err: %v", err)
		}
		log.Info("DKG sync to", "round", c.dkgSnapshot.Round-1, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
	}

	// DKG checkpoint handling, also syncs the dkg process if keystore is out-of-date
	if currentHeight >= shareStartHeight && !c.dkgSnapshot.ShareTasked {
		// Send share and reshare tx when currentHeight == shareStartHeight
		c.dkgSnapshot.PendingCNs, err = getPendingConsensus(c.ethAPI, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch pending consensus: %v", err)
		}
		// Prepare and start a DKG
		if !c.amevKeystore.IsSharing() {
			if err = c.amevKeystore.OnSharePeriodStart(); err != nil {
				return fmt.Errorf("failed to start new DKG, err: %v", err)
			}
		}
		// If the period finished, then skip sending a transaction
		if currentHeight < recoverStartHeight {
			// If is a member of pending consensus
			indexOfSharing := slices.Index(c.dkgSnapshot.PendingCNs, amevAddress) + 1
			receiverMessageKeys, err := getMessagePubkeys(c.ethAPI, c.dkgSnapshot.PendingCNs, state, h)
			if err != nil {
				return fmt.Errorf("failed to get message keys, err: %v", err)
			}
			if indexOfSharing > 0 {
				if err = c.taskShare(receiverMessageKeys, currentHeight, recoverStartHeight, watchList); err != nil {
					return fmt.Errorf("failed to task DKG share, err: %v", err)
				}
			}
			// If is a member of current consensus, try reshare but give up if error
			indexOfResharing := slices.Index(c.dkgSnapshot.CurrentCNs, amevAddress) + 1
			if indexOfResharing > 0 && c.dkgSnapshot.Round > 1 {
				if err = c.taskReshare(receiverMessageKeys, currentHeight, recoverStartHeight, watchList); err != nil {
					c.dkgSnapshot.ShareTasked = true
					return fmt.Errorf("failed to task DKG reshare, err: %v", err)
				}
			}
		}
		c.dkgSnapshot.ShareTasked = true
	}

	if currentHeight >= recoverStartHeight && !c.dkgSnapshot.RecoverTasked {
		// Check isShareReady at height recoverStartHeight
		ready, err := isShareReady(c.ethAPI, state, h)
		if err != nil {
			return fmt.Errorf("failed to check if sharing is ready: %v", err)
		}
		if !ready {
			c.dkgSnapshot.RecoverTasked = true
			log.Warn("DKG sharing is not ready, skip recover")
			return nil
		}
		// If share is ready, pending consensus nodes should ReceiveSecretShare
		indexOfSharing := slices.Index(c.dkgSnapshot.PendingCNs, amevAddress) + 1
		if indexOfSharing > 0 {
			if err := c.syncThisRoundSecrets(c.dkgSnapshot, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync sharing DKG, err: %v", err)
			}
		}
		c.dkgSnapshot.IndexNeedRecover, err = getIndexCurrentNeedRecovering(c.ethAPI, state, h)
		if err != nil {
			return fmt.Errorf("failed to fecth index need recovering, err: %v", err)
		}
		// If the period finished, then skip sending a transaction
		if len(c.dkgSnapshot.IndexNeedRecover) > 0 {
			// Only indexesNeedRecover <= (consensusSize - threshold) can recover
			threshold := consensusSize - (consensusSize-1)/3
			if len(c.dkgSnapshot.IndexNeedRecover) > int(consensusSize-threshold) {
				c.dkgSnapshot.RecoverTasked = true
				log.Warn("DKG resharing doesn't meet recoverable threshold, skip recover")
				return nil
			}
			if !c.amevKeystore.IsRecovering() {
				if err := c.amevKeystore.OnRecoverPeriodStart(); err != nil {
					return fmt.Errorf("failed to start DKG recover, err: %v", err)
				}
			}
			if currentHeight < recoverCheckHeight {
				// Send recover tx from current consensus node
				indexOfResharing := slices.Index(c.dkgSnapshot.CurrentCNs, amevAddress) + 1
				if indexOfResharing > 0 {
					pubs := make([]*ecies.PublicKey, len(c.dkgSnapshot.IndexNeedRecover))
					for i, index := range c.dkgSnapshot.IndexNeedRecover {
						pubs[i], err = getMessagePubkey(c.ethAPI, c.dkgSnapshot.PendingCNs[index-1], state, h)
						if err != nil {
							return fmt.Errorf("failed to get message keys for recovering, err: %v", err)
						}
					}
					if err := c.taskRecover(c.dkgSnapshot.IndexNeedRecover, pubs, currentHeight, recoverCheckHeight, watchList); err != nil {
						return fmt.Errorf("failed to task DKG recover, err: %v", err)
					}
				}
			}
		}
		c.dkgSnapshot.RecoverTasked = true
	}

	// Keystore should anyway be up-to-date here
	if c.amevKeystore.IsRecovering() && currentHeight >= recoverCheckHeight && !c.dkgSnapshot.ReshareRecoverTasked {
		// Send reshareRecovered at height recoverStartHeigh+c.shareDuration/2
		// Only index in indexsNeedRecover and pending consensus node need to call reshareRecovered
		indexOfSharing := slices.Index(c.dkgSnapshot.PendingCNs, amevAddress) + 1
		if indexOfSharing > 0 && currentHeight < targetHeight && slices.Contains(c.dkgSnapshot.IndexNeedRecover, uint64(indexOfSharing)) {
			if err := c.syncRecoveredSecrets(c.dkgSnapshot, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync recovering DKG, err: %v", err)
			}
			receiverMessageKeys, err := getMessagePubkeys(c.ethAPI, c.dkgSnapshot.PendingCNs, state, h)
			if err != nil {
				return fmt.Errorf("failed to fecth message keys, err: %v", err)
			}
			if err := c.taskReshareRecover(receiverMessageKeys, currentHeight, targetHeight, watchList); err != nil {
				return fmt.Errorf("failed to task DKG reshare recover, err: %v", err)
			}
		}
		c.dkgSnapshot.ReshareRecoverTasked = true
	}
	return nil
}

// loopTaskList retries every task in tx watch list
func (c *DBFT) loopTaskList() {
	for watchList := range c.loopTaskChan {
		log.Info("DKG loopTaskList", "CurrentHeight", watchList.CurrentHeight, "WatchListLength", len(watchList.WatchList))
		currentHeight := watchList.CurrentHeight
		// append watchList from loopTaskChan to c.txWatchList
		for i := range watchList.WatchList {
			c.txWatchList = append(c.txWatchList, &watchList.WatchList[i])
		}

		// loop tasks in c.txWatchList
		var retryList []*TxWatchRetry
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
						if receipt["status"] != types.ReceiptStatusSuccessful {
							needRetry = true
							log.Error("DKG get transaction receipt status error", "txHash", item.TxHash, "status", receipt["status"])
						}
					}

					var err error
					if needRetry {
						item.TxHash, err = sendTxToKeyManagement(c.txAPI, c.signer, item.Method, item.Params...)
						if err != nil {
							retryList = append(retryList, item)
							log.Error("DKG retry sending transaction failed", "currentHeight", currentHeight, "method", item.Method, "err", err)
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
}

// syncLastRoundSecrets downloads DKG sharings and related PVSS, and sends to keystore
func (c *DBFT) syncLastRoundSecrets(snap *Snapshot, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveShare(snap.Round-1, i+1, selfIndex, state, header); err != nil {
			return err
		}
		if snap.Round > 1 {
			if err := c.downloadAndReceiveReshare(snap.Round-1, i+1, selfIndex, state, header); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncThisRoundSecrets downloads DKG sharings and related PVSS, and sends to keystore
func (c *DBFT) syncThisRoundSecrets(snap *Snapshot, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.PendingCNs {
		if err := c.downloadAndReceiveShare(snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
	}
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveReshare(snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
	}
	return nil
}

func (c *DBFT) downloadAndReceiveShare(round uint64, fromIndex int, selfIndex int, state *state.StateDB, header *types.Header) error {
	// Call ReceiveSecretShare
	shareMsgs, err := getShareMsgs(c.ethAPI, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	spvss, err := getSharePVSS(c.ethAPI, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	err = c.amevKeystore.ReceiveSecretShare(selfIndex, fromIndex, shareMsgs, spvss)
	if err != nil {
		return err
	}
	return nil
}

func (c *DBFT) downloadAndReceiveReshare(round uint64, fromIndex int, selfIndex int, state *state.StateDB, header *types.Header) error {
	// Call ReceiveSecretReshare
	rpvss, err := getResharePVSS(c.ethAPI, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	// Only receive reshare has value
	if len(rpvss) > 0 {
		reshareMsgs, err := getReshareMsgs(c.ethAPI, round, uint64(fromIndex), state, header)
		if err != nil {
			return err
		}
		err = c.amevKeystore.ReceiveSecretReshare(selfIndex, fromIndex, reshareMsgs, rpvss)
		if err != nil {
			return err
		}
	}
	return nil
}

// syncRecoveredSecrets downloads DKG recoverings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredSecrets(snap *Snapshot, selfIndex int, state *state.StateDB, header *types.Header) error {
	pvss, err := getSharePVSS(c.ethAPI, snap.Round-1, uint64(selfIndex), state, header)
	if err != nil {
		return err
	}
	for i := range snap.CurrentCNs {
		msg, err := getRecoverMsgs(c.ethAPI, snap.Round, uint64(i+1), uint64(selfIndex-1), state, header)
		if err != nil {
			return err
		}
		if len(msg) > 0 {
			err = c.amevKeystore.ReceiveRecoverShare(selfIndex, i+1, msg, pvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncRecoveredReshares downloads recovered DKG resharings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredReshares(snap *Snapshot, selfIndex int, state *state.StateDB, header *types.Header) error {
	for _, index := range snap.IndexNeedRecover {
		// Call ReceiveSecretReshare
		rpvss, err := getResharePVSS(c.ethAPI, snap.Round, index, state, header)
		if err != nil {
			return err
		}
		if len(rpvss) > 0 {
			reshareMsgs, err := getReshareMsgs(c.ethAPI, snap.Round, index, state, header)
			if err != nil {
				return err
			}
			err = c.amevKeystore.ReceiveSecretReshare(selfIndex, int(index), reshareMsgs, rpvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// taskShare tries to send secret shares as a transaction
func (c *DBFT) taskShare(receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	sMsgs, sPvss, err := c.amevKeystore.DKGShare(receiverMessageKeys)
	if err != nil {
		return err
	}
	// Send share tx
	txHash, err := sendShareTx(c.txAPI, c.signer, sPvss, sMsgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "share", Params: []interface{}{sPvss, sMsgs}}
	if err != nil {
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Error("failed to send share transaction", "err", err)
	} else {
		txWatch.TxHash = txHash
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Info("DKG share transaction sent", "txHash", txHash)
	}
	return nil
}

// taskReshare tries to send secret reshares as a transaction
func (c *DBFT) taskReshare(receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	rMsgs, rPvss, err := c.amevKeystore.DKGReshare(receiverMessageKeys)
	if err != nil {
		return err
	}
	// Send reshare tx
	txHash, err := sendReshareTx(c.txAPI, c.signer, rPvss, rMsgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "reshare", Params: []interface{}{rPvss, rMsgs}}
	if err != nil {
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Error("failed to send reshare transaction", "err", err)
	} else {
		txWatch.TxHash = txHash
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Info("DKG reshare transaction sent", "txHash", txHash)
	}
	return nil
}

// taskRecover tries to send past secret shares as a transaction
func (c *DBFT) taskRecover(indexesNeedRecover []uint64, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	var idxsInt []int
	for _, idx := range indexesNeedRecover {
		idxsInt = append(idxsInt, int(idx))
	}
	msgs, err := c.amevKeystore.DKGRecover(idxsInt, receiverMessageKeys)
	if err != nil {
		return err
	}
	// Send recover tx
	txHash, err := sendRecoverTx(c.txAPI, c.signer, idxsInt, msgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "recover", Params: []interface{}{indexesNeedRecover, msgs}}
	if err != nil {
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Error("failed to send recover transaction", "err", err)
	} else {
		txWatch.TxHash = txHash
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Info("DKG recover transaction sent", "txHash", txHash)
	}
	return nil
}

// taskReshareRecover tries to send recovered secret reshares as a transaction
func (c *DBFT) taskReshareRecover(receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	// Recover the lost resharing messages
	msgs, pvss, err := c.amevKeystore.TryRecoverReshare(receiverMessageKeys)
	if err != nil {
		return err
	}
	// Send reshareRecovered tx
	txHash, err := sendReshareRecoveredTx(c.txAPI, c.signer, pvss, msgs)
	txWatch := &TxWatchRetry{SendHeight: start, EndHeight: end, Method: "reshareRecovered", Params: []interface{}{pvss, msgs}}
	if err != nil {
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Error("failed to send reshareRecovered transaction", "err", err)
	} else {
		txWatch.TxHash = txHash
		watchList.WatchList = append(watchList.WatchList, *txWatch)
		log.Info("DKG reshareRecovered transaction sent", "txHash", txHash)
	}
	return nil
}

// getCurrentConsensus returns an address list of current CNs
func getCurrentConsensus(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	result, err := readFromContract(api, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getCurrentConsensus")
	if err != nil {
		return nil, err
	}
	return result.([]common.Address), nil
}

// getPendingConsensus returns an address list of pending CNs
func getPendingConsensus(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	result, err := readFromContract(api, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getPendingConsensus")
	if err != nil {
		return nil, err
	}
	return result.([]common.Address), nil
}

// getSharePeriodDuration returns a number of blocks as the duration of each sharing period
func getSharePeriodDuration(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) (uint64, error) {
	result, err := readFromContract(api, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "sharePeriodDuration")
	if err != nil {
		return 0, err
	}
	return result.(*big.Int).Uint64(), nil
}

// getEpochDuration returns a number of blocks as the duration of each governanace epoch
func getEpochDuration(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) (uint64, error) {
	result, err := readFromContract(api, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "epochDuration")
	if err != nil {
		return 0, err
	}
	return result.(*big.Int).Uint64(), nil
}

// getCurrentEpochStartHeight returns the block height when the current governanace epoch starts
func getCurrentEpochStartHeight(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) (uint64, error) {
	result, err := readFromContract(api, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "currentEpochStartHeight")
	if err != nil {
		return 0, err
	}
	return result.(*big.Int).Uint64(), nil
}

// getMessagePubkeys returns the message keys of input address list
func getMessagePubkeys(api *ethapi.BlockChainAPI, addrs []common.Address, state *state.StateDB, header *types.Header) ([]*ecies.PublicKey, error) {
	result := make([]*ecies.PublicKey, len(addrs))
	for i, addr := range addrs {
		pub, err := getMessagePubkey(api, addr, state, header)
		if err != nil {
			return nil, err
		}
		result[i] = pub
	}
	return result, nil
}

// getMessagePubkey returns the message key of input address
func getMessagePubkey(api *ethapi.BlockChainAPI, addr common.Address, state *state.StateDB, header *types.Header) (*ecies.PublicKey, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "messagePubkeys", addr)
	if err != nil {
		return nil, err
	}
	if len(result.(string)) == 0 {
		err = errors.New("messagePubkey is empty, addr: " + addr.String())
		return nil, err
	}
	key, err := crypto.UnmarshalPubkey([]byte(result.(string)))
	if err != nil {
		return nil, err
	}
	return ecies.ImportECDSAPublic(key), nil
}

// getIndexCurrentNeedRecovering returns an array of DKG index that needs recover
func getIndexCurrentNeedRecovering(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) ([]uint64, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "indexCurrentNeedRecovering")
	if err != nil {
		return nil, err
	}
	var indexs []uint64
	for _, item := range result.([]*big.Int) {
		indexs = append(indexs, item.Uint64())
	}
	return indexs, nil
}

// isShareReady checks if the DKG sharing is 100% uploaded
func isShareReady(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) (bool, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "isShareReady")
	if err != nil {
		return false, err
	}
	return result.(bool), nil
}

func getReshareMsgs(api *ethapi.BlockChainAPI, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getReshareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result.([][]byte), nil
}

func getResharePVSS(api *ethapi.BlockChainAPI, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "rpvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

func getShareMsgs(api *ethapi.BlockChainAPI, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getShareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result.([][]byte), nil
}

func getSharePVSS(api *ethapi.BlockChainAPI, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "spvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

func getRecoverMsgs(api *ethapi.BlockChainAPI, round, senderIndex, arrIndex uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "recoverMsgs", big.NewInt(int64(round)), big.NewInt(int64(senderIndex)), big.NewInt(int64(arrIndex)))
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

func getAggregatedCommitment(api *ethapi.BlockChainAPI, round uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "aggregatedCommitments", big.NewInt(int64(round)))
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

func getRoundNumber(api *ethapi.BlockChainAPI, state *state.StateDB, header *types.Header) (uint64, error) {
	result, err := readFromContract(api, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "roundNumber")
	if err != nil {
		return 0, err
	}
	return result.(*big.Int).Uint64(), nil
}

func readFromContract(api *ethapi.BlockChainAPI, contract common.Address, contractAbi abi.ABI, state *state.StateDB, header *types.Header, method string, args ...interface{}) (interface{}, error) {
	if api == nil {
		return nil, errors.New("eth blockchain API is not initialized, DKG can't function properly")
	}
	data, err := contractAbi.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to pack '%s': %v", method, err)
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
	result, err := api.CallAtState(ctx, txArgs, state, header)
	if err != nil {
		return nil, fmt.Errorf("failed to call at state '%s': %v", method, err)
	}
	results, err := contractAbi.Unpack(method, result)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack result: %v", err)
	}
	return results[0], nil
}

func sendReshareTx(api *ethapi.TransactionAPI, from common.Address, pvss []byte, messages [][]byte) (*common.Hash, error) {
	return sendTxToKeyManagement(api, from, "reshare", pvss, messages)
}

func sendShareTx(api *ethapi.TransactionAPI, from common.Address, pvss []byte, messages [][]byte) (*common.Hash, error) {
	return sendTxToKeyManagement(api, from, "share", pvss, messages)
}

func sendReshareRecoveredTx(api *ethapi.TransactionAPI, from common.Address, pvss []byte, messages [][]byte) (*common.Hash, error) {
	return sendTxToKeyManagement(api, from, "reshareRecovered", pvss, messages)
}

func sendRecoverTx(api *ethapi.TransactionAPI, from common.Address, idxs []int, messages [][]byte) (*common.Hash, error) {
	var idxsBigInt []*big.Int
	for _, idx := range idxs {
		idxsBigInt = append(idxsBigInt, big.NewInt(int64(idx)))
	}
	return sendTxToKeyManagement(api, from, "recover", idxsBigInt, messages)
}

func sendTxToKeyManagement(api *ethapi.TransactionAPI, signer common.Address, method string, args ...interface{}) (*common.Hash, error) {
	if api == nil {
		return nil, errors.New("eth transaction API is not initialized, DKG can't function properly")
	}
	data, err := systemcontracts.KeyManagementABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to pack '%s': %v", method, err)
	}
	msgData := hexutil.Bytes(data)

	txHash, err := api.SendTransaction(context.Background(),
		ethapi.TransactionArgs{
			From: &signer,
			To:   &systemcontracts.KeyManagementProxyHash,
			Data: &msgData})

	if err != nil {
		return nil, fmt.Errorf("failed to send tx with consensus node, to %s data: '%s': %v", systemcontracts.KeyManagementProxyHash, hex.EncodeToString(data), err)
	}
	return &txHash, nil
}
