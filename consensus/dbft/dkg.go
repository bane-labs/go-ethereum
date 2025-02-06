package dbft

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	zkdkg "github.com/bane-labs/zk-dkg"
	"github.com/bane-labs/zk-dkg/circuit"
	"github.com/bane-labs/zk-dkg/helper"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/secp256k1"
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

const (
	Phase1File            = "Phase1"
	Phase2FileForOneMsg   = "Phase2_1"
	Phase2FileForTwoMsg   = "Phase2_2"
	Phase2FileForFourMsg  = "Phase2_4"
	Phase2FileForSevenMsg = "Phase2_7"
)

// task is a struct to prepare and send a DKG transaction
type task struct {
	SendHeight       uint64
	EndHeight        uint64
	TxHash           *common.Hash
	Method           string
	Params           []interface{}
	ConfirmedSuccess bool
}

// taskList is a watch task list send to loopTransactionTaskList by channel
type taskList []*task

// append adds a new element to the task list
func (l *taskList) append(t *task) {
	arr := append(*l, t)
	l = &arr
}

// snapshot is a temporary record to save progress of a DKG round
type snapshot struct {
	EpochStartHeight     uint64
	Round                uint64 // Starts from 1, points to the next round if initDone.
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
func NewSnapshot() *snapshot {
	return &snapshot{}
}

// Copy creates a copy of Snapshot.
func (s *snapshot) Copy() *snapshot {
	cp := *s

	cp.CurrentCNs = slices.Clone(s.CurrentCNs)
	cp.PendingCNs = slices.Clone(s.PendingCNs)
	cp.IndexNeedRecover = slices.Clone(s.IndexNeedRecover)

	return &cp
}

// init initializes snapshot with the specified startup parameters.
func (s *snapshot) init(api *ethapi.Backend, h *types.Header, state *state.StateDB, height uint64) error {
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
func (s *snapshot) reset() {
	*s = snapshot{}
}

// handleDKG handles the transaction submission for DKG process.
// It constructs and sends transaction to KeyManagement contract using amev store.
func (c *DBFT) handleDKG(snapshot *snapshot, keystore *antimev.KeyStore, h *types.Header, state *state.StateDB, suspended bool) error {
	currentHeight := h.Number.Uint64()
	taskList := (taskList)(make([]*task, 0))
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
		if !suspended {
			log.Info("DKG reached targetHeight", "round", snapshot.Round, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		}
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
		if !suspended {
			log.Info("DKG info", "roundNumber", snapshot.Round, "epochStartHeight", snapshot.EpochStartHeight, "epochDuration", epochDuration,
				"sharePeriodDuration", sharePeriodDuration, "consensusList", snapshot.CurrentCNs)
		}
	}
	// Compute periods based on realtime data, in case there is an update in governanace contract
	targetHeight := snapshot.EpochStartHeight + epochDuration
	shareStartHeight := targetHeight - 2*sharePeriodDuration
	recoverStartHeight := shareStartHeight + sharePeriodDuration
	recoverCheckHeight := recoverStartHeight + sharePeriodDuration/2
	consensusSize := uint64(len(snapshot.CurrentCNs))

	if !suspended && currentHeight >= shareStartHeight && currentHeight < targetHeight {
		// Send pending task list to execution when handleDKG finished
		defer func() {
			c.executeProofTaskChan <- &taskList
		}()
	}

	// If keystore is out-of-date, then sync shared DKG up-tp-date
	keystoreRound := keystore.Round()
	// If keystore has a round of future, then return an error
	if keystoreRound > int(snapshot.Round)-1 {
		return fmt.Errorf("invalid antimev keystore round index: expected %d, got %d", snapshot.Round-1, keystoreRound)
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
		if !suspended {
			log.Info("DKG sync to", "round", snapshot.Round-1, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		}
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
				if err = taskList.taskReshare(keystore, receiverMessageKeys, currentHeight, recoverStartHeight); err != nil {
					return fmt.Errorf("failed to task DKG reshare, err: %v", err)
				}
			}
			// If is a member of pending consensus
			indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
			if err != nil {
				return fmt.Errorf("failed to get message keys, err: %v", err)
			}
			if indexOfSharing > 0 {
				if err = taskList.taskShare(keystore, receiverMessageKeys, currentHeight, recoverStartHeight); err != nil {
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
							if err := taskList.taskRecover(keystore, snapshot.IndexNeedRecover, pubs, currentHeight, recoverCheckHeight); err != nil {
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
			if err := taskList.taskReshareRecover(keystore, receiverMessageKeys, currentHeight, targetHeight); err != nil {
				return fmt.Errorf("failed to task DKG reshare recover, err: %v", err)
			}
		}
		snapshot.ReshareRecoverTasked = true
	}

	if !suspended {
		if err := keystore.Persist(); err != nil {
			return fmt.Errorf("failed to persist keystore, err: %v", err)
		}
	}
	return nil
}

// loopExecuteProofTask executes ZK proof generation if there is a task
func (c *DBFT) loopExecuteProofTask() {
	log.Info("DKG proof dispatcher started")
	defer log.Info("DKG proof dispatcher stopped")

	for pendingList := range c.executeProofTaskChan {
		currentHeight := (*c.backend).CurrentBlock().Number.Uint64()
		log.Info("DKG loopExecuteProofTask", "currentHeight", currentHeight, "pendingListLength", len(*pendingList))
		// Handle new tasks immediately but one by one
		watchList := (taskList)(make([]*task, 0))
		for _, task := range *pendingList {
			switch task.Method {
			case "share", "reshare", "reshareRecovered":
				// Prove the encryption of seven new messages
				ss := task.Params[0].([]*big.Int)
				pvss := task.Params[1].([]byte)
				pubs := task.Params[2].([]*ecies.PublicKey)
				fis := make([]fr.Element, len(ss))
				for i, s := range ss {
					fis[i].SetBigInt(s)
				}
				if len(fis) != len(pubs) {
					log.Error("share task array not match", "secrets", len(fis), "keys", len(pubs))
					continue
				}
				// Compute necessary inputs for circuit
				fisBytes, fisInts, bigFis, nonces, encryptedFis, rs, bigRs := circuit.PrepareEncryptedKeyShares(pubs, fis)
				// Compute zk proof
				_, proof, _, err := zkdkg.ProveMultipleKeyShareEncryption(Phase1File, Phase2FileForSevenMsg, pubs, rs, bigRs, fisBytes, fisInts, bigFis, encryptedFis, nonces)
				if err != nil {
					log.Error("failed to prove DKG", "method", task.Method)
					continue
				}
				// Update the task parameters for sending a transaction
				msgs := encodeMessages(encryptedFis, bigRs, nonces)
				proofData, cmts, cmtPok := helper.GetContractInput(proof)
				// Send the transaction
				txHash, err := sendTransactionToKeyManagement(c.txAPI, c.signer, task.Method, pvss, msgs, proofData, cmts, cmtPok)
				if err != nil {
					log.Error("failed to send DKG transaction", "err", err)
				} else {
					task.TxHash = txHash
					log.Info("DKG transaction sent", "txHash", txHash)
				}
				task.Params = []interface{}{pvss, encryptedFis, proofData}
				// Add the transation to retry list
				watchList.append(task)
			case "recover":
				// Prove the encryption of old share messages and new messages, possibly 2 or 4 in total
				indexes := task.Params[0].([]uint64)
				ss := task.Params[1].([]*big.Int)
				pubs := task.Params[2].([]*ecies.PublicKey)
				fis := make([]fr.Element, len(ss))
				for i, s := range ss {
					fis[i].SetBigInt(s)
				}
				if len(fis) != len(pubs) || len(fis) != len(indexes) {
					log.Error("recover task array not match", "indexes", len(indexes), "secrets", len(fis), "keys", len(pubs))
					continue
				}
				// Compute necessary inputs for circuit
				fisBytes, fisInts, bigFis, nonces, encryptedFis, rs, bigRs := circuit.PrepareEncryptedKeyShares(pubs, fis)
				// Compute zk proof
				var phase2Path string
				switch len(indexes) {
				case 1:
					phase2Path = Phase2FileForOneMsg
				case 2:
					phase2Path = Phase2FileForTwoMsg
				default:
					log.Error("unexpected amount of secrets to prove", "amount", len(indexes))
					continue
				}
				_, proof, _, err := zkdkg.ProveMultipleKeyShareEncryption(Phase1File, phase2Path, pubs, rs, bigRs, fisBytes, fisInts, bigFis, encryptedFis, nonces)
				if err != nil {
					log.Error("failed to prove DKG", "method", task.Method)
					continue
				}
				// Update the task parameters for sending a transaction
				msgs := encodeMessages(encryptedFis, bigRs, nonces)
				proofData, cmts, cmtPok := helper.GetContractInput(proof)
				// Send the transaction
				txHash, err := sendRecoverTransaction(c.txAPI, c.signer, indexes, msgs, proofData, cmts, cmtPok)
				if err != nil {
					log.Error("failed to send DKG transaction", "err", err)
				} else {
					task.TxHash = txHash
					log.Info("DKG transaction sent", "txHash", txHash)
				}
				task.Params = []interface{}{indexes, encryptedFis, proofData}
				// Add the transation to retry list
				watchList.append(task)
			default:
				log.Error("unknown DKG method to prove", "method", task.Method)
				continue
			}
		}
		c.loopTransactionTaskChan <- &watchList
	}
}

// loopTransactionTaskList retries every transaction task in transaction watch list
func (c *DBFT) loopTransactionTaskList() {
	log.Info("DKG transaction dispatcher started")
	defer log.Info("DKG transaction dispatcher stopped")

	for watchList := range c.loopTransactionTaskChan {
		currentHeight := (*c.backend).CurrentBlock().Number.Uint64()
		log.Info("DKG loopTransactionTaskList", "currentHeight", currentHeight, "watchListLength", len(*watchList))
		// Append watchList from loopTransactionTaskChan to c.transactionTaskList
		c.transactionTaskList = append(c.transactionTaskList, *watchList...)

		// Loop tasks in c.transactionTaskList
		var retryList []*task
		if len(c.transactionTaskList) > 0 {
			for _, item := range c.transactionTaskList {
				if currentHeight < item.EndHeight && !item.ConfirmedSuccess {
					needRetry := false
					// Send failed, just resend and set transaction hash
					if item.TxHash == nil {
						needRetry = true
					}

					// Send successfully, wait 3 blocks to check transaction status
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
						item.TxHash, err = sendTransactionToKeyManagement(c.txAPI, c.signer, item.Method, item.Params...)
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
			c.transactionTaskList = retryList
		}
	}
}

// syncLastRoundSecrets downloads DKG sharings and related PVSS, and sends to keystore
func (c *DBFT) syncLastRoundSecrets(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
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
func (c *DBFT) syncThisRoundSecrets(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
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
func (c *DBFT) syncRecoveredSecrets(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
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
func (c *DBFT) syncRecoveredReshares(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
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
func (t *taskList) taskShare(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64) error {
	secrets, sPvss, err := keystore.DKGShare()
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs
	proofTask := &task{SendHeight: start, EndHeight: end, Method: "share", Params: []interface{}{secrets, sPvss, receiverMessageKeys}}
	t.append(proofTask)
	return nil
}

// taskReshare tries to send secret reshares as a transaction
func (t *taskList) taskReshare(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64) error {
	secrets, rPvss, err := keystore.DKGReshare()
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs
	proofTask := &task{SendHeight: start, EndHeight: end, Method: "reshare", Params: []interface{}{secrets, rPvss, receiverMessageKeys}}
	t.append(proofTask)
	return nil
}

// taskRecover tries to send past secret shares as a transaction
func (t *taskList) taskRecover(keystore *antimev.KeyStore, indexesNeedRecover []uint64, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64) error {
	var idxsInt []int
	for _, idx := range indexesNeedRecover {
		idxsInt = append(idxsInt, int(idx))
	}
	secrets, err := keystore.DKGRecover(idxsInt)
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs
	proofTask := &task{SendHeight: start, EndHeight: end, Method: "recover", Params: []interface{}{indexesNeedRecover, secrets, receiverMessageKeys}}
	t.append(proofTask)
	return nil
}

// taskReshareRecover tries to send recovered secret reshares as a transaction
func (t *taskList) taskReshareRecover(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, start uint64, end uint64) error {
	// Recover the lost resharing messages
	secrets, pvss, err := keystore.TryRecoverReshare()
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs
	proofTask := &task{SendHeight: start, EndHeight: end, Method: "reshareRecovered", Params: []interface{}{secrets, pvss, receiverMessageKeys}}
	t.append(proofTask)
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

func sendRecoverTransaction(api *ethapi.TransactionAPI, from common.Address, idxs []uint64, messages [][]byte, proof [8]*big.Int, cmts []*big.Int, cmtPok [2]*big.Int) (*common.Hash, error) {
	var idxsBigInt []*big.Int
	for _, idx := range idxs {
		idxsBigInt = append(idxsBigInt, big.NewInt(int64(idx)))
	}
	return sendTransactionToKeyManagement(api, from, "recover", idxsBigInt, messages, proof, cmts, cmtPok)
}

func sendTransactionToKeyManagement(api *ethapi.TransactionAPI, signer common.Address, method string, args ...interface{}) (*common.Hash, error) {
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
		return nil, fmt.Errorf("failed to send transaction with consensus node, to %s data: '%s': %v", systemcontracts.KeyManagementProxyHash, hex.EncodeToString(data), err)
	}
	return &txHash, nil
}

func encodeMessages(encryptedFis [][]byte, bigRs []secp256k1.G1Affine, nonces [][]byte) [][]byte {
	result := make([][]byte, 0)
	for i := range encryptedFis {
		bigRBytes := bigRs[i].RawBytes()
		prefix := append(bigRBytes[:], nonces[i]...)
		result = append(result, append(prefix, encryptedFis[i]...))
	}
	return result
}
