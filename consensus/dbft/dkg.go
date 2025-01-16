package dbft

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/antimev"
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

	// initDone denotes whether snapshot initialization procedure has passed.
	initDone bool
}

// NewSnapshot creates the new instance of DKG snapshot.
func NewSnapshot() *Snapshot {
	return &Snapshot{}
}

// Copy creates a copy of Snapshot.
func (s *Snapshot) Copy() *Snapshot {
	cp := *s

	cp.CurrentCNs = slices.Clone(s.CurrentCNs)
	cp.PendingCNs = slices.Clone(s.PendingCNs)
	cp.IndexNeedRecover = slices.Clone(s.IndexNeedRecover)

	return &cp
}

// init initializes snapshot with the specified startup parameters.
func (s *Snapshot) init(api *ethapi.Backend, h *types.Header, state *state.StateDB, height uint64) error {
	s.EpochStartHeight = height
	round, err := getRoundNumber(api, state, h)
	if err != nil {
		return err
	}
	// Snapshot round index points to the new round, so plus 1
	s.Round = round + 1
	s.CurrentCNs, err = getCurrentConsensus(api, state, h)
	if err != nil {
		return err
	}
	s.PendingCNs = make([]common.Address, 0)
	s.IndexNeedRecover = make([]uint64, 0)
	s.ShareTasked = false
	s.RecoverTasked = false
	s.ReshareRecoverTasked = false
	s.initDone = true
	return nil
}

// reset resets snapshot state to default.
func (s *Snapshot) reset() {
	*s = Snapshot{}
}

// handleDKG handles the transaction submission for DKG process.
// It constructs and sends transaction to KeyManagement contract using amev store.
func (c *DBFT) handleDKG(snapshot *Snapshot, keystore *antimev.KeyStore, h *types.Header, state *state.StateDB, suspended bool) error {
	currentHeight := h.Number.Uint64()
	watchList := &TxWatchList{
		CurrentHeight: currentHeight,
		WatchList:     make([]TxWatchRetry, 0),
	}
	amevAddress := keystore.Address()
	if state == nil {
		s, err := c.chain.StateAt(h.Root)
		if err != nil {
			return fmt.Errorf("failed to call StateAt: %v", err)
		}
		state = s
	}
	epochDuration, err := getEpochDuration(c.backend, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch epoch duration: %v", err)
	}
	sharePeriodDuration, err := getSharePeriodDuration(c.backend, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch share period duration: %v", err)
	}

	// If there is an ongoing round and it's time to epoch change
	if snapshot.initDone && currentHeight == snapshot.EpochStartHeight+epochDuration {
		indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
		// Call ReceiveRecoveredReshare at targetHeight, only at this block height we can get aggregatedCommitments
		if len(snapshot.IndexNeedRecover) > 0 && indexOfSharing > 0 {
			if err := c.syncRecoveredReshares(snapshot, keystore, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync recovered secrets, err: %v", err)
			}
		}
		// if indexOfSharing is 0, then selfPvss should be nil
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113
		selfPvss, err := getSharePVSS(c.backend, snapshot.Round, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvss, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.backend, snapshot.Round, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		err = keystore.OnEpochChange(selfPvss, aggregatedCommitment, indexOfSharing > 0)
		if err != nil {
			return fmt.Errorf("failed to change keystore epoch, err: %v", err)
		}
		log.Info("DKG reached targetHeight", "round", snapshot.Round, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		snapshot.reset()
	}

	// If there is not a snapshot of current epoch, then new
	epochStartHeight, err := getCurrentEpochStartHeight(c.backend, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch current epoch start height: %v", err)
	}
	if !snapshot.initDone {
		err = snapshot.init(c.backend, h, state, epochStartHeight)
		if err != nil {
			return fmt.Errorf("failed to new DKG snapshot, err: %v", err)
		}
		log.Info("DKG info", "roundNumber", snapshot.Round, "epochStartHeight", snapshot.EpochStartHeight, "epochDuration", epochDuration,
			"sharePeriodDuration", sharePeriodDuration, "consensusList", snapshot.CurrentCNs)
	}
	// Compute periods based on realtime data, in case there is an update in governanace contract
	targetHeight := snapshot.EpochStartHeight + epochDuration
	shareStartHeight := targetHeight - 2*sharePeriodDuration
	recoverStartHeight := shareStartHeight + sharePeriodDuration
	recoverCheckHeight := recoverStartHeight + sharePeriodDuration/2
	consensusSize := uint64(len(snapshot.CurrentCNs))

	if !suspended && currentHeight >= shareStartHeight && currentHeight < targetHeight {
		// Send watch task list to loopTaskChan when handleDKG finished
		defer func() {
			c.loopTaskChan <- watchList
		}()
	}

	// If keystore is out-of-date, then sync shared DKG up-tp-date
	keystoreRound := keystore.Round()
	// If keystore has a round of future, then return an error
	if keystoreRound >= int(snapshot.Round) {
		return fmt.Errorf("invalid antimev keystore round index: %v", keystoreRound)
	}
	// If this round failed but keystore is still in a sharing state
	if keystoreRound == int(snapshot.Round)-1 && currentHeight < shareStartHeight && keystore.IsSharing() {
		// Here only revert before the new share period, if the node wake up when share,
		// then data will be overwritten
		keystore.RevertRound()
	}
	// Sync if keystore needs a sync and contract has at least 1 round successful DKG
	if keystoreRound < int(snapshot.Round)-1 {
		// If keystore is late for more than 1 round, than reset it to the last one,
		// past data are not helpful anymore
		if keystoreRound < int(snapshot.Round)-2 {
			keystore.Reset(int(snapshot.Round) - 2)
		}
		// If keystore is in a sharing state already, then regard it has a valid secret
		if !keystore.IsSharing() {
			keystore.OnSharePeriodStart()
		}
		// If is a member of current consensus, then try sync secrets
		indexOfSharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
		if indexOfSharing > 0 {
			// Only a warning here, since it doesn't destroy dBFT and anti-mev,
			// a new DKG can perform and the next reshare is recoverable.
			// But it is dangerous if more than 1/3 CNs reinit their keystores
			// or change their message key.
			if err := c.syncLastRoundSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
				log.Warn("failed to sync shared DKG", "err", err)
			}
		}
		// if indexOfSharing is 0, then selfPvss should be nil
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113
		selfPvss, err := getSharePVSS(c.backend, snapshot.Round-1, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvsses, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.backend, snapshot.Round-1, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		if err := keystore.OnEpochChange(selfPvss, aggregatedCommitment, indexOfSharing > 0); err != nil {
			return fmt.Errorf("failed to sync shared DKG, err: %v", err)
		}
		log.Info("DKG sync to", "round", snapshot.Round-1, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
	}

	// DKG checkpoint handling, also syncs the dkg process if keystore is out-of-date
	if currentHeight >= shareStartHeight && !snapshot.ShareTasked {
		// Send share and reshare tx when currentHeight == shareStartHeight
		snapshot.PendingCNs, err = getPendingConsensus(c.backend, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch pending consensus: %v", err)
		}
		// Prepare and start a DKG
		if !keystore.IsSharing() {
			keystore.OnSharePeriodStart()
		}
		// If the period finished, then skip sending a transaction
		if !suspended && currentHeight < recoverStartHeight {
			// If is a member of current consensus, try reshare but give up if error
			indexOfResharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
			receiverMessageKeys, err := getMessagePubkeys(c.backend, snapshot.PendingCNs, state, h)
			if indexOfResharing > 0 && snapshot.Round > 1 {
				if err = c.taskReshare(keystore, receiverMessageKeys, currentHeight, recoverStartHeight, watchList); err != nil {
					return fmt.Errorf("failed to task DKG reshare, err: %v", err)
				}
			}
			// If is a member of pending consensus
			indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
			if err != nil {
				return fmt.Errorf("failed to get message keys, err: %v", err)
			}
			if indexOfSharing > 0 {
				if err = c.taskShare(keystore, receiverMessageKeys, currentHeight, recoverStartHeight, watchList); err != nil {
					return fmt.Errorf("failed to task DKG share, err: %v", err)
				}
			}
		}
		snapshot.ShareTasked = true
	}

	if currentHeight >= recoverStartHeight && !snapshot.RecoverTasked {
		// Check isShareReady at height recoverStartHeight
		ready, err := isShareReady(c.backend, state, h)
		if err != nil {
			return fmt.Errorf("failed to check if sharing is ready: %v", err)
		}
		if ready {
			// If share is ready, pending consensus nodes should ReceiveSecretShare
			indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
			if indexOfSharing > 0 {
				if err := c.syncThisRoundSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
					return fmt.Errorf("failed to sync sharing DKG, err: %v", err)
				}
			}
			snapshot.IndexNeedRecover, err = getIndexCurrentNeedRecovering(c.backend, state, h)
			if err != nil {
				return fmt.Errorf("failed to fetch index need recovering, err: %v", err)
			}
			// If the period finished, then skip sending a transaction
			if len(snapshot.IndexNeedRecover) > 0 {
				// Only indexesNeedRecover <= (consensusSize - threshold) can recover
				threshold := consensusSize - (consensusSize-1)/3
				if len(snapshot.IndexNeedRecover) <= int(consensusSize-threshold) {
					if !keystore.IsRecovering() {
						keystore.OnRecoverPeriodStart()
					}
					if !suspended && currentHeight < recoverCheckHeight {
						// Send recover tx from current consensus node
						indexOfResharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
						if indexOfResharing > 0 {
							pubs := make([]*ecies.PublicKey, len(snapshot.IndexNeedRecover))
							for i, index := range snapshot.IndexNeedRecover {
								pubs[i], err = getMessagePubkey(c.backend, snapshot.PendingCNs[index-1], state, h)
								if err != nil {
									return fmt.Errorf("failed to get message keys for recovering, err: %v", err)
								}
							}
							if err := c.taskRecover(keystore, snapshot.IndexNeedRecover, pubs, currentHeight, recoverCheckHeight, watchList); err != nil {
								return fmt.Errorf("failed to task DKG recover, err: %v", err)
							}
						}
					}
				}
			}
		}
		snapshot.RecoverTasked = true
	}

	// Keystore should anyway be up-to-date here
	if keystore.IsRecovering() && currentHeight >= recoverCheckHeight && !snapshot.ReshareRecoverTasked {
		// Send reshareRecovered at height recoverStartHeigh+c.shareDuration/2
		// Only index in indexsNeedRecover and pending consensus node need to call reshareRecovered
		indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
		if !suspended && indexOfSharing > 0 && currentHeight < targetHeight && slices.Contains(snapshot.IndexNeedRecover, uint64(indexOfSharing)) {
			if err := c.syncRecoveredSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync recovering DKG, err: %v", err)
			}
			receiverMessageKeys, err := getMessagePubkeys(c.backend, snapshot.PendingCNs, state, h)
			if err != nil {
				return fmt.Errorf("failed to fecth message keys, err: %v", err)
			}
			if err := c.taskReshareRecover(keystore, receiverMessageKeys, currentHeight, targetHeight, watchList); err != nil {
				return fmt.Errorf("failed to task DKG reshare recover, err: %v", err)
			}
		}
		snapshot.ReshareRecoverTasked = true
	}

	if err := keystore.Persist(); err != nil {
		return fmt.Errorf("failed to persist keystore, err: %v", err)
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
func (c *DBFT) syncLastRoundSecrets(snap *Snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveShare(keystore, snap.Round-1, i+1, selfIndex, state, header); err != nil {
			return err
		}
		if snap.Round > 1 {
			if err := c.downloadAndReceiveReshare(keystore, snap.Round-1, i+1, selfIndex, state, header); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncThisRoundSecrets downloads DKG sharings and related PVSS, and sends to keystore
func (c *DBFT) syncThisRoundSecrets(snap *Snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.PendingCNs {
		if err := c.downloadAndReceiveShare(keystore, snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
	}
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveReshare(keystore, snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
	}
	return nil
}

func (c *DBFT) downloadAndReceiveShare(keystore *antimev.KeyStore, round uint64, fromIndex int, selfIndex int, state *state.StateDB, header *types.Header) error {
	// Call ReceiveSecretShare
	shareMsgs, err := getShareMsgs(c.backend, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	spvss, err := getSharePVSS(c.backend, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	err = keystore.ReceiveSecretShare(selfIndex, fromIndex, shareMsgs, spvss)
	if err != nil {
		return err
	}
	return nil
}

func (c *DBFT) downloadAndReceiveReshare(keystore *antimev.KeyStore, round uint64, fromIndex int, selfIndex int, state *state.StateDB, header *types.Header) error {
	// Call ReceiveSecretReshare
	rpvss, err := getResharePVSS(c.backend, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	// Only receive reshare has value
	if len(rpvss) > 0 {
		reshareMsgs, err := getReshareMsgs(c.backend, round, uint64(fromIndex), state, header)
		if err != nil {
			return err
		}
		err = keystore.ReceiveSecretReshare(selfIndex, fromIndex, reshareMsgs, rpvss)
		if err != nil {
			return err
		}
	}
	return nil
}

// syncRecoveredSecrets downloads DKG recoverings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredSecrets(snap *Snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	pvss, err := getSharePVSS(c.backend, snap.Round-1, uint64(selfIndex), state, header)
	if err != nil {
		return err
	}
	for i := range snap.CurrentCNs {
		msg, err := getRecoverMsgs(c.backend, snap.Round, uint64(i+1), uint64(selfIndex-1), state, header)
		if err != nil {
			return err
		}
		if len(msg) > 0 {
			err = keystore.ReceiveRecoverShare(selfIndex, i+1, msg, pvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncRecoveredReshares downloads recovered DKG resharings and related PVSS, and sends to keystore
func (c *DBFT) syncRecoveredReshares(snap *Snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	for _, index := range snap.IndexNeedRecover {
		// Call ReceiveSecretReshare
		rpvss, err := getResharePVSS(c.backend, snap.Round, index, state, header)
		if err != nil {
			return err
		}
		if len(rpvss) > 0 {
			reshareMsgs, err := getReshareMsgs(c.backend, snap.Round, index, state, header)
			if err != nil {
				return err
			}
			err = keystore.ReceiveSecretReshare(selfIndex, int(index), reshareMsgs, rpvss)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// taskShare tries to send secret shares as a transaction
func (c *DBFT) taskShare(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	sMsgs, sPvss, err := keystore.DKGShare(receiverMessageKeys)
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
func (c *DBFT) taskReshare(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	rMsgs, rPvss, err := keystore.DKGReshare(receiverMessageKeys)
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
func (c *DBFT) taskRecover(keystore *antimev.KeyStore, indexesNeedRecover []uint64, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	var idxsInt []int
	for _, idx := range indexesNeedRecover {
		idxsInt = append(idxsInt, int(idx))
	}
	msgs, err := keystore.DKGRecover(idxsInt, receiverMessageKeys)
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
func (c *DBFT) taskReshareRecover(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64, watchList *TxWatchList) error {
	// Recover the lost resharing messages
	msgs, pvss, err := keystore.TryRecoverReshare(receiverMessageKeys)
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
func getCurrentConsensus(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getCurrentConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getPendingConsensus returns an address list of pending CNs
func getPendingConsensus(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getPendingConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getSharePeriodDuration returns a number of blocks as the duration of each sharing period
func getSharePeriodDuration(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "sharePeriodDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getEpochDuration returns a number of blocks as the duration of each governanace epoch
func getEpochDuration(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "epochDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getCurrentEpochStartHeight returns the block height when the current governanace epoch starts
func getCurrentEpochStartHeight(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "currentEpochStartHeight")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getMessagePubkeys returns the message keys of input address list
func getMessagePubkeys(backend *ethapi.Backend, addrs []common.Address, state *state.StateDB, header *types.Header) ([]*ecies.PublicKey, error) {
	result := make([]*ecies.PublicKey, len(addrs))
	for i, addr := range addrs {
		pub, err := getMessagePubkey(backend, addr, state, header)
		if err != nil {
			return nil, err
		}
		result[i] = pub
	}
	return result, nil
}

// getMessagePubkey returns the message key of input address
func getMessagePubkey(backend *ethapi.Backend, addr common.Address, state *state.StateDB, header *types.Header) (*ecies.PublicKey, error) {
	var result string
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "messagePubkeys", addr)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		err = errors.New("messagePubkey is empty, addr: " + addr.String())
		return nil, err
	}
	key, err := crypto.UnmarshalPubkey([]byte(result))
	if err != nil {
		return nil, err
	}
	return ecies.ImportECDSAPublic(key), nil
}

// getIndexCurrentNeedRecovering returns an array of DKG index that needs recover
func getIndexCurrentNeedRecovering(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]uint64, error) {
	var result []*big.Int
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "indexCurrentNeedRecovering")
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
func isShareReady(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (bool, error) {
	var result bool
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI, state, header, "isShareReady")
	if err != nil {
		return false, err
	}
	return result, nil
}

func getReshareMsgs(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getReshareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getResharePVSS(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "rpvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getShareMsgs(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "getShareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getSharePVSS(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "spvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getRecoverMsgs(backend *ethapi.Backend, round, senderIndex, arrIndex uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "recoverMsgs", big.NewInt(int64(round)), big.NewInt(int64(senderIndex)), big.NewInt(int64(arrIndex)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getAggregatedCommitment(backend *ethapi.Backend, round uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "aggregatedCommitments", big.NewInt(int64(round)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getRoundNumber(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABI,
		state, header, "roundNumber")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

func readFromContract(res interface{}, backend *ethapi.Backend, contract common.Address, contractAbi abi.ABI, state *state.StateDB, header *types.Header, method string, args ...interface{}) error {
	if backend == nil {
		return errors.New("eth API backend is not initialized, DKG can't function properly")
	}
	data, err := contractAbi.Pack(method, args...)
	if err != nil {
		return fmt.Errorf("failed to pack '%s': %v", method, err)
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
	result, err := ethapi.DoCallAtState(ctx, *backend, txArgs, state, header, nil, nil, 0, 0)
	if err != nil {
		return fmt.Errorf("failed to call at state '%s': %v", method, err)
	}
	return unpackContractExecutionResult(res, result, contractAbi, method)
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
