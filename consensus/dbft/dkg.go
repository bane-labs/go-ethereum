package dbft

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	zkdkg "github.com/bane-labs/zk-dkg"
	"github.com/bane-labs/zk-dkg/circuit"
	"github.com/bane-labs/zk-dkg/helper"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/secp256k1"
	groth16 "github.com/consensys/gnark/backend/groth16/bn254"
	"github.com/consensys/gnark/constraint"
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

// task is a struct to prepare and send a DKG transaction.
type task struct {
	SendHeight       uint64
	EndHeight        uint64
	TxHash           *common.Hash
	Method           string
	ZKVersion        uint64
	Params           []interface{}
	ConfirmedSuccess bool
}

// taskList is a watch task list send to loopTransactionTaskList by channel.
type taskList []*task

// snapshot is a temporary record to save progress of a DKG round.
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
	// Snapshot round index points to the new round, so plus 1.
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
	taskList := make(taskList, 0)
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
	zkVersion, err := getZKVersion(c.backend, state, h)
	if err != nil {
		return fmt.Errorf("failed to fetch ZK version: %v", err)
	}

	// If there is an ongoing round and it's time to epoch change.
	if snapshot.initDone && currentHeight >= snapshot.EpochStartHeight+epochDuration {
		indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
		if indexOfSharing > 0 {
			if err := c.syncThisRoundSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync sharing DKG, err: %v", err)
			}
		}
		// if indexOfSharing is 0, then selfPvss should be nil.
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113.
		selfPvss, err := getSharePVSS(c.backend, snapshot.Round, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvss, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.backend, snapshot.Round, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		lastCommitment, err := getAggregatedCommitment(c.backend, snapshot.Round-1, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		err = keystore.OnEpochChange(selfPvss, aggregatedCommitment, lastCommitment, indexOfSharing > 0)
		if err != nil {
			return fmt.Errorf("failed to change keystore epoch, err: %v", err)
		}
		if !suspended {
			log.Info("DKG reached targetHeight", "round", snapshot.Round, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		}
		snapshot.reset()
	}

	// If there is not a snapshot of current epoch, then new.
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
	// Compute periods based on realtime data, in case there is an update in governanace contract.
	targetHeight := snapshot.EpochStartHeight + epochDuration
	shareStartHeight := targetHeight - 2*sharePeriodDuration
	recoverStartHeight := shareStartHeight + sharePeriodDuration
	recoverCheckHeight := recoverStartHeight + sharePeriodDuration/2
	consensusSize := uint64(len(snapshot.CurrentCNs))

	// If keystore is out-of-date, then sync shared DKG up-tp-date.
	keystoreRound := keystore.Round()
	// If keystore has a round of future, then return an error.
	if keystoreRound > int(snapshot.Round)-1 {
		return fmt.Errorf("invalid antimev keystore round index: expected %d, got %d", snapshot.Round-1, keystoreRound)
	}
	// If this round failed but keystore is still in a sharing state.
	if keystoreRound == int(snapshot.Round)-1 && currentHeight < shareStartHeight && keystore.IsSharing() {
		// Here only revert before the new share period, if the node wake up when share,
		// then data will be overwritten.
		keystore.RevertRound()
	}
	// Sync if keystore needs a sync and contract has at least 1 round successful DKG.
	if keystoreRound < int(snapshot.Round)-1 {
		// If keystore is late for more than 1 round, than reset it to the last one,
		// past data are not helpful anymore.
		if keystoreRound < int(snapshot.Round)-2 {
			keystore.Reset(int(snapshot.Round) - 2)
		}
		// If keystore is in a sharing state already, then regard it has a valid secret.
		if !keystore.IsSharing() {
			// Enforce to init an empty resharing when necessary.
			keystore.OnSharePeriodStart(snapshot.Round > 2)
		}
		// If is a member of current consensus, then try sync secrets.
		indexOfSharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
		if indexOfSharing > 0 {
			// Only a warning here, since it doesn't destroy dBFT and anti-mev,
			// a new DKG can perform and the next reshare is recoverable.
			// But it is dangerous if more than 1/3 CNs change their message key.
			if err := c.syncLastRoundSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
				log.Warn("failed to sync shared DKG", "err", err)
			} else {
				// If the message synchronization succeeds, then the message key remains the same.
				// It means the local secret is also recoverable. Here we just do share with the
				// same network id and round number, then we should get the same result.
				// The result will be checked in epoch change, log an error message if no secret
				// can not be confirmed in committed PVSS.
				_, _, err := keystore.DKGShare((*c.backend).ChainConfig().ChainID)
				if err != nil {
					return fmt.Errorf("failed to replay sharing, err: %v", err)
				}
			}
		}
		// If indexOfSharing is 0, then selfPvss should be nil.
		// Ref https://github.com/bane-labs/go-ethereum/blob/4c9105ea2bc246729db0540fce2df02074e21087/contracts/solidity/KeyManagement.sol#L113.
		selfPvss, err := getSharePVSS(c.backend, snapshot.Round-1, uint64(indexOfSharing), state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch spvsses, err: %v", err)
		}
		aggregatedCommitment, err := getAggregatedCommitment(c.backend, snapshot.Round-1, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
		}
		var lastCommitment []byte
		if snapshot.Round > 2 {
			lastCommitment, err = getAggregatedCommitment(c.backend, snapshot.Round-2, state, h)
			if err != nil {
				return fmt.Errorf("failed to fetch aggregated commitment, err: %v", err)
			}
		}
		if err := keystore.OnEpochChange(selfPvss, aggregatedCommitment, lastCommitment, indexOfSharing > 0); err != nil {
			return fmt.Errorf("failed to sync shared DKG, err: %v", err)
		}
		if !suspended {
			log.Info("DKG sync to", "round", snapshot.Round-1, "currentHeight", currentHeight, "aggregatedCommitment", hex.EncodeToString(aggregatedCommitment))
		}
	}

	// DKG checkpoint handling, also syncs the dkg process if keystore is out-of-date.
	if currentHeight >= shareStartHeight && !snapshot.ShareTasked {
		// Send share and reshare tx when currentHeight == shareStartHeight.
		snapshot.PendingCNs, err = getPendingConsensus(c.backend, state, h)
		if err != nil {
			return fmt.Errorf("failed to fetch pending consensus: %v", err)
		}
		// Prepare and start a DKG.
		if !keystore.IsSharing() {
			keystore.OnSharePeriodStart(false)
		}
		// If the period finished, then skip sending a transaction.
		if !suspended {
			// If is a member of current consensus, try reshare but give up if error.
			indexOfResharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
			receiverMessageKeys, err := getMessagePubkeys(c.backend, snapshot.PendingCNs, state, h)
			if err != nil {
				return fmt.Errorf("failed to get message keys, err: %v", err)
			}
			if indexOfResharing > 0 && snapshot.Round > 1 {
				err = taskList.taskReshare(keystore, receiverMessageKeys, zkVersion, currentHeight, recoverStartHeight)
				if err != nil {
					log.Error("Failed to task DKG reshare", "err", err)
				}
			}
			// If is a member of pending consensus.
			indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
			if indexOfSharing > 0 {
				err = taskList.taskShare(keystore, (*c.backend).ChainConfig().ChainID, receiverMessageKeys, zkVersion, currentHeight, recoverStartHeight)
				if err != nil {
					return fmt.Errorf("failed to task DKG share, err: %v", err)
				}
			}
		}
		snapshot.ShareTasked = true
	}

	if currentHeight >= recoverStartHeight && !snapshot.RecoverTasked {
		// Check isShareReady at height recoverStartHeight.
		ready, err := isShareReady(c.backend, state, h)
		if err != nil {
			return fmt.Errorf("failed to check if sharing is ready: %v", err)
		}
		if ready {
			snapshot.IndexNeedRecover, err = getIndexCurrentNeedRecovering(c.backend, state, h)
			if err != nil {
				return fmt.Errorf("failed to fetch index need recovering, err: %v", err)
			}
			// If no recover is required, then skip sending a transaction.
			if len(snapshot.IndexNeedRecover) > 0 {
				// Only indexesNeedRecover <= (consensusSize - threshold) can recover.
				threshold := consensusSize - (consensusSize-1)/3
				if len(snapshot.IndexNeedRecover) <= int(consensusSize-threshold) {
					if !keystore.IsRecovering() {
						keystore.OnRecoverPeriodStart()
					}
					if !suspended {
						// Send recover tx from current consensus node.
						indexOfResharing := slices.Index(snapshot.CurrentCNs, amevAddress) + 1
						if indexOfResharing > 0 {
							pubs := make([]*ecies.PublicKey, len(snapshot.IndexNeedRecover))
							for i, index := range snapshot.IndexNeedRecover {
								pubs[i], err = getMessagePubkey(c.backend, snapshot.PendingCNs[index-1], state, h)
								if err != nil {
									return fmt.Errorf("failed to get message keys for recovering, err: %v", err)
								}
							}
							err = taskList.taskRecover(keystore, snapshot.IndexNeedRecover, pubs, zkVersion, currentHeight, recoverCheckHeight)
							if err != nil {
								return fmt.Errorf("failed to task DKG recover, err: %v", err)
							}
						}
					}
				}
			}
		}
		snapshot.RecoverTasked = true
	}

	// Keystore should anyway be up-to-date here.
	if keystore.IsRecovering() && currentHeight >= recoverCheckHeight && !snapshot.ReshareRecoverTasked {
		// Send reshareRecovered at height recoverStartHeigh+c.shareDuration/2.
		// Only index in indexsNeedRecover and pending consensus node need to call reshareRecovered.
		indexOfSharing := slices.Index(snapshot.PendingCNs, amevAddress) + 1
		if !suspended && indexOfSharing > 0 && slices.Contains(snapshot.IndexNeedRecover, uint64(indexOfSharing)) {
			if err := c.syncRecoveredSecrets(snapshot, keystore, indexOfSharing, state, h); err != nil {
				return fmt.Errorf("failed to sync recovering DKG, err: %v", err)
			}
			receiverMessageKeys, err := getMessagePubkeys(c.backend, snapshot.PendingCNs, state, h)
			if err != nil {
				return fmt.Errorf("failed to fecth message keys, err: %v", err)
			}
			err = taskList.taskReshareRecover(keystore, receiverMessageKeys, zkVersion, currentHeight, targetHeight)
			if err != nil {
				return fmt.Errorf("failed to task DKG reshare recover, err: %v", err)
			}
		}
		snapshot.ReshareRecoverTasked = true
	}

	if !suspended {
		if err := keystore.Persist(); err != nil {
			return fmt.Errorf("failed to persist keystore, err: %v", err)
		}
		// Notify dkgTaskExecutorCh iff there's new task.
		if len(taskList) > 0 {
			select {
			case <-c.quit:
				return nil
			case c.dkgTaskExecutorCh <- &taskList:
			default:
				// Give up the tasks since the channel is going to block DKG and dBFT.
				return fmt.Errorf("DKG task executor channel is full")
			}
		}
		// Always notify dkgTaskWatcherCh since it needs heartbit.
		select {
		case <-c.quit:
			return nil
		case c.dkgTaskWatcherCh <- nil:
		default:
			// Use non-blocking send since if dkgTaskWatcherCh is busy with processing
			// some other tasks, then triggering it one more time is completely useless.
			log.Info("failed to send heartbit signal to DKG task watcher: handler is busy, will retry after the next block",
				"index", h.Number.Uint64())
		}
	}
	return nil
}

// dkgTaskExecutor executes ZK proof generation if there is a task.
func (c *DBFT) dkgTaskExecutor() {
	log.Info("DKG task executor started")

executeLoop:
	for {
		select {
		case <-c.quit:
			break executeLoop
		case pendingList := <-c.dkgTaskExecutorCh:
			var watchList taskList
			log.Info("DKG task executor", "pendingListLength", len(*pendingList))

			// Share and reshare are always in the same task list for proving, so here is a cache to reduce file read.
			// 1 and 2 message proving happen at most once per epoch, and not together with a 7, so no cache for them.
			// A task list to prove is consist of 7+7 (length is 2), 1 or 2 or 7 (length is 1), no other case for now.
			// So this cache should not alloc memory when a 1 or 2 message proving is going to happen.
			var (
				sevenMsgR1CS constraint.ConstraintSystem
				sevenMsgPK   *groth16.ProvingKey
			)

			// Handle new tasks immediately but one by one.
			for _, task := range *pendingList {
				switch task.Method {
				case "share", "reshare", "reshareRecovered":
					// Prove the encryption of seven new messages.
					ss := task.Params[0].([]*big.Int)
					pvss := task.Params[1].([]byte)
					pubs := task.Params[2].([]*ecies.PublicKey)
					if len(ss) != len(pubs) {
						panic(fmt.Errorf("%s task array mismatch: secrets %d, keys %d", task.Method, len(ss), len(pubs)))
					}
					// Convert types.
					fis := make([]*fr.Element, len(ss))
					for i, s := range ss {
						fis[i] = new(fr.Element).SetBigInt(s)
					}
					// Compute necessary inputs for circuit.
					fisBytes, fisInts, bigFis, nonces, encryptedFis, rs, bigRs, err := circuit.PrepareEncryptedKeyShares(pubs, fis)
					if err != nil {
						log.Error("failed to prepare encrypted key shares", "err", err, "method", task.Method)
						continue
					}
					// Update the task parameters for sending a transaction.
					msgs := encodeMessages(encryptedFis, bigRs, nonces)
					// Send transactions based on ZK settings.
					switch task.ZKVersion {
					case 0:
						// Send the transaction.
						txHash, err := sendTransactionToKeyManagement(c.txAPI, c.signer, task.Method, task.ZKVersion, pvss, msgs)
						if err != nil {
							log.Error("failed to send DKG transaction", "err", err)
						} else {
							task.TxHash = txHash
							log.Info("DKG transaction sent", "txHash", txHash)
						}
						task.Params = []interface{}{pvss, msgs}
					case 1:
						// The circuit setup is limited for proofs of 7-message tasks, so disable zk for a privnet in different sizes.
						if len(fis) != 7 {
							panic(fmt.Errorf("invalid number of %s message inputs: expect 7, get %d", task.Method, len(fis)))
						}
						var err error
						if sevenMsgR1CS == nil {
							sevenMsgR1CS, err = helper.ReadCSS(c.zkFiles.sevenMsgR1CSPath)
							if err != nil {
								log.Error("invalid r1cs file", "file", c.zkFiles.sevenMsgR1CSPath, "err", err)
								continue
							}
						}
						if sevenMsgPK == nil {
							sevenMsgPK, err = helper.ReadProvingKey(c.zkFiles.sevenMsgProvingKeyPath)
							if err != nil {
								log.Error("invalid proving key file", "file", c.zkFiles.sevenMsgProvingKeyPath, "err", err)
								continue
							}
						}
						// Compute zk proof.
						proof, _, err := zkdkg.ProveMultipleKeyShareEncryption(sevenMsgR1CS, sevenMsgPK, pubs, rs, bigRs, fisBytes, fisInts, bigFis, encryptedFis, nonces)
						if err != nil {
							log.Error("failed to prove DKG", "method", task.Method)
							continue
						}
						proofData, cmts, cmtPok := helper.GetContractInput(proof)
						// Send the transaction.
						txHash, err := sendTransactionToKeyManagement(c.txAPI, c.signer, task.Method, task.ZKVersion, pvss, msgs, proofData, cmts, cmtPok)
						if err != nil {
							log.Error("failed to send DKG transaction", "err", err)
						} else {
							task.TxHash = txHash
							log.Info("DKG transaction sent", "txHash", txHash)
						}
						task.Params = []interface{}{pvss, msgs, proofData, cmts, cmtPok}
					default:
						log.Error("Unsupported ZK version", "version", task.ZKVersion)
						continue
					}
					// Add the transation to retry list.
					watchList = append(watchList, task)
				case "recover":
					// Prove the encryption of old share messages and new messages, possibly 2 or 4 in total.
					indexes := task.Params[0].([]uint64)
					ss := task.Params[1].([]*big.Int)
					pubs := task.Params[2].([]*ecies.PublicKey)
					if len(ss) != len(pubs) || len(ss) != len(indexes) {
						panic(fmt.Errorf("%s task array mismatch: indexes %d, secrets %d, keys %d", task.Method, len(indexes), len(ss), len(pubs)))
					}
					// Convert types.
					fis := make([]*fr.Element, len(ss))
					idxsBigInt := make([]*big.Int, len(indexes))
					for i, s := range ss {
						fis[i] = new(fr.Element).SetBigInt(s)
					}
					for i, idx := range indexes {
						idxsBigInt[i] = big.NewInt(int64(idx))
					}
					// Compute necessary inputs for circuit.
					fisBytes, fisInts, bigFis, nonces, encryptedFis, rs, bigRs, err := circuit.PrepareEncryptedKeyShares(pubs, fis)
					if err != nil {
						log.Error("failed to prepare encrypted key shares", "err", err, "method", task.Method)
						continue
					}
					// Update the task parameters for sending a transaction.
					msgs := encodeMessages(encryptedFis, bigRs, nonces)
					// Send transactions based on ZK settings.
					switch task.ZKVersion {
					case 0:
						// Send the transaction.
						txHash, err := sendTransactionToKeyManagement(c.txAPI, c.signer, task.Method, task.ZKVersion, idxsBigInt, msgs)
						if err != nil {
							log.Error("failed to send DKG transaction", "err", err)
						} else {
							task.TxHash = txHash
							log.Info("DKG transaction sent", "txHash", txHash)
						}
						task.Params = []interface{}{idxsBigInt, msgs}
					case 1:
						// Compute zk proof.
						var (
							r1cs       constraint.ConstraintSystem
							provingKey *groth16.ProvingKey
							err        error
						)
						switch len(indexes) {
						case 1:
							r1cs, err = helper.ReadCSS(c.zkFiles.oneMsgR1CSPath)
							if err != nil {
								log.Error("invalid r1cs file", "file", c.zkFiles.oneMsgR1CSPath, "err", err)
								continue
							}
							provingKey, err = helper.ReadProvingKey(c.zkFiles.oneMsgProvingKeyPath)
							if err != nil {
								log.Error("invalid proving key file", "file", c.zkFiles.oneMsgProvingKeyPath, "err", err)
								continue
							}
						case 2:
							r1cs, err = helper.ReadCSS(c.zkFiles.twoMsgR1CSPath)
							if err != nil {
								log.Error("invalid r1cs file", "file", c.zkFiles.twoMsgR1CSPath, "err", err)
								continue
							}
							provingKey, err = helper.ReadProvingKey(c.zkFiles.twoMsgProvingKeyPath)
							if err != nil {
								log.Error("invalid proving key file", "file", c.zkFiles.twoMsgProvingKeyPath, "err", err)
								continue
							}
						default:
							// The circuit setup is limited for proofs of 1-or-2-message tasks, other cases shouldn't happen.
							panic(fmt.Errorf("invalid number of %s message inputs: expect 1 or 2, get %d", task.Method, len(fis)))
						}
						proof, _, err := zkdkg.ProveMultipleKeyShareEncryption(r1cs, provingKey, pubs, rs, bigRs, fisBytes, fisInts, bigFis, encryptedFis, nonces)
						if err != nil {
							log.Error("failed to prove DKG", "method", task.Method)
							continue
						}
						proofData, cmts, cmtPok := helper.GetContractInput(proof)
						// Send the transaction.
						txHash, err := sendTransactionToKeyManagement(c.txAPI, c.signer, task.Method, task.ZKVersion, idxsBigInt, msgs, proofData, cmts, cmtPok)
						if err != nil {
							log.Error("failed to send DKG transaction", "err", err)
						} else {
							task.TxHash = txHash
							log.Info("DKG transaction sent", "txHash", txHash)
						}
						task.Params = []interface{}{idxsBigInt, msgs, proofData, cmts, cmtPok}
						// Release immediately after proving.
						r1cs = nil
						provingKey = nil
					default:
						log.Error("Unsupported ZK version", "version", task.ZKVersion)
						continue
					}
					// Add the transation to retry list.
					watchList = append(watchList, task)
				default:
					panic(fmt.Errorf("unknown DKG method to prove: %s", task.Method))
				}
			}
			// Release the cache when the task list is fully handled.
			sevenMsgR1CS = nil
			sevenMsgPK = nil

			select {
			case <-c.quit:
				break executeLoop
			case c.dkgTaskWatcherCh <- &watchList:
				// Use blocking send since transactions have been constructed and sent, we need to
				// notify dkgTaskWatcherCh anyway even if it will block dBFT operation.
			}
		}
	}

drainLoop:
	for {
		select {
		case <-c.dkgTaskExecutorCh:
		default:
			break drainLoop
		}
	}

	close(c.dkgTaskExecutorToCloseCh)
	log.Info("DKG task executor stopped")
}

// dkgTaskWatcher retries every transaction task in transaction watch list.
func (c *DBFT) dkgTaskWatcher() {
	log.Info("DKG task watcher started")

	var watchTaskList taskList
watchLoop:
	for {
		select {
		case <-c.quit:
			break watchLoop
		case newWatchList := <-c.dkgTaskWatcherCh:
			// If it's a heartbit, then just recheck the old tasks. If newWatchList is non-empty, then
			// update the watchlist.
			if newWatchList != nil && len(*newWatchList) > 0 {
				watchTaskList = append(watchTaskList, *newWatchList...)
			}
			currentHeight := (*c.backend).CurrentBlock().Number.Uint64()
			log.Info("DKG task watcher", "currentHeight", currentHeight, "watchListLength", len(watchTaskList))

			// Loop tasks in watchTaskList.
			var retryList taskList
			if len(watchTaskList) > 0 {
				for _, item := range watchTaskList {
					if currentHeight < item.EndHeight && !item.ConfirmedSuccess {
						needRetry := false
						// Send failed, just resend and set transaction hash.
						if item.TxHash == nil {
							needRetry = true
						}

						// Send successfully, wait 3 blocks to check transaction status.
						if item.TxHash != nil && currentHeight-item.SendHeight == 3 {
							receipt, err := c.txAPI.GetTransactionReceipt(context.Background(), *item.TxHash)
							if err != nil {
								needRetry = true
								log.Error("DKG get transaction receipt failed", "err", err, "txHash", item.TxHash)
							} else if receipt["status"] != types.ReceiptStatusSuccessful {
								needRetry = true
								log.Error("DKG get transaction receipt status error", "txHash", item.TxHash, "status", receipt["status"])
							}
						}

						var err error
						if needRetry {
							item.TxHash, err = sendTransactionToKeyManagement(c.txAPI, c.signer, item.Method, item.ZKVersion, item.Params...)
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
				// Only keep retry failed and not reach max retry times.
				watchTaskList = retryList
			}
		}
	}

drainLoop:
	for {
		select {
		case <-c.dkgTaskWatcherCh:
		default:
			break drainLoop
		}
	}

	close(c.dkgTaskWatcherToCloseCh)
	log.Info("DKG task watcher stopped")
}

// syncLastRoundSecrets downloads DKG sharings and related PVSS, and sends to keystore.
func (c *DBFT) syncLastRoundSecrets(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveShare(keystore, snap.Round-1, i+1, selfIndex, state, header); err != nil {
			return err
		}
		if snap.Round > 2 {
			if err := c.downloadAndReceiveReshare(keystore, snap.Round-1, i+1, selfIndex, state, header); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncThisRoundSecrets synchronizes DKG data in this round to update the local anti-mev keystore for the first stage.
func (c *DBFT) syncThisRoundSecrets(snap *snapshot, keystore *antimev.KeyStore, selfIndex int, state *state.StateDB, header *types.Header) error {
	for i := range snap.CurrentCNs {
		if err := c.downloadAndReceiveShare(keystore, snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
		if err := c.downloadAndReceiveReshare(keystore, snap.Round, i+1, selfIndex, state, header); err != nil {
			return err
		}
	}
	return nil
}

// downloadAndReceiveShare downloads DKG messages and related PVSS in sharing, and sends to keystore.
func (c *DBFT) downloadAndReceiveShare(keystore *antimev.KeyStore, round uint64, fromIndex int, selfIndex int, state *state.StateDB, header *types.Header) error {
	// Call ReceiveSecretShare
	spvss, err := getSharePVSS(c.backend, round, uint64(fromIndex), state, header)
	if err != nil {
		return err
	}
	// Only receive share has value
	if len(spvss) > 0 {
		shareMsgs, err := getShareMsgs(c.backend, round, uint64(fromIndex), state, header)
		if err != nil {
			return err
		}
		err = keystore.ReceiveSecretShare(selfIndex, fromIndex, shareMsgs, spvss)
		if err != nil {
			return err
		}
	}
	return nil
}

// downloadAndReceiveReshare downloads DKG messages and related PVSS in resharing, and sends to keystore.
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

// syncRecoveredSecrets synchronizes DKG data in this round to update the local anti-mev keystore for the second stage.
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

// taskShare tries to prepare necessary inputs for a contract calling for DKG share.
func (t *taskList) taskShare(keystore *antimev.KeyStore, networkId *big.Int, receiverMessageKeys []*ecies.PublicKey, zkVersion uint64, start uint64, end uint64) error {
	// Generate or regenerate the local secrets.
	secrets, sPvss, err := keystore.DKGShare(networkId)
	if err != nil {
		return err
	}
	// Call keystore to share before skip, in case there is a synchronization.
	if start >= end {
		return nil
	}
	// Set up a new task with keystore outputs.
	*t = append(*t, &task{SendHeight: start, EndHeight: end, Method: "share", ZKVersion: zkVersion, Params: []interface{}{secrets, sPvss, receiverMessageKeys}})
	return nil
}

// taskReshare tries to prepare necessary inputs for a contract calling for DKG reshare.
func (t *taskList) taskReshare(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, zkVersion uint64, start uint64, end uint64) error {
	// If is later than the period deadline, skip.
	if start >= end {
		return nil
	}
	secrets, rPvss, err := keystore.DKGReshare()
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs.
	*t = append(*t, &task{SendHeight: start, EndHeight: end, Method: "reshare", ZKVersion: zkVersion, Params: []interface{}{secrets, rPvss, receiverMessageKeys}})
	return nil
}

// taskRecover tries to prepare necessary inputs for a contract calling for DKG recover.
func (t *taskList) taskRecover(keystore *antimev.KeyStore, indexesNeedRecover []uint64, receiverMessageKeys []*ecies.PublicKey, zkVersion uint64, start uint64, end uint64) error {
	// If is later than the target deadline, skip.
	if start >= end {
		return nil
	}
	var idxsInt []int
	for _, idx := range indexesNeedRecover {
		idxsInt = append(idxsInt, int(idx))
	}
	secrets, err := keystore.DKGRecover(idxsInt)
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs.
	*t = append(*t, &task{SendHeight: start, EndHeight: end, Method: "recover", ZKVersion: zkVersion, Params: []interface{}{indexesNeedRecover, secrets, receiverMessageKeys}})
	return nil
}

// taskReshareRecover tries to prepare necessary inputs for a contract calling for DKG reshare after recover.
func (t *taskList) taskReshareRecover(keystore *antimev.KeyStore, receiverMessageKeys []*ecies.PublicKey, zkVersion uint64, start uint64, end uint64) error {
	// If is later than the period deadline, skip.
	if start >= end {
		return nil
	}
	// Recover the lost resharing messages.
	secrets, pvss, err := keystore.TryRecoverReshare()
	if err != nil {
		return err
	}
	// Set up a new task with keystore outputs.
	*t = append(*t, &task{SendHeight: start, EndHeight: end, Method: "reshareRecovered", ZKVersion: zkVersion, Params: []interface{}{secrets, pvss, receiverMessageKeys}})
	return nil
}

// getCurrentConsensus returns an address list of current CNs.
func getCurrentConsensus(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getCurrentConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getPendingConsensus returns an address list of pending CNs.
func getPendingConsensus(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]common.Address, error) {
	var result []common.Address
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "getPendingConsensus")
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getSharePeriodDuration returns a number of blocks as the duration of each sharing period.
func getSharePeriodDuration(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "sharePeriodDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getEpochDuration returns a number of blocks as the duration of each governanace epoch.
func getEpochDuration(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "epochDuration")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getCurrentEpochStartHeight returns the block height when the current governanace epoch starts.
func getCurrentEpochStartHeight(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.GovernanceProxyHash, systemcontracts.GovernanceABI, state, header, "currentEpochStartHeight")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getMessagePubkeys returns the message keys of input address list.
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

// getMessagePubkey returns the message key of input address.
func getMessagePubkey(backend *ethapi.Backend, addr common.Address, state *state.StateDB, header *types.Header) (*ecies.PublicKey, error) {
	var result string
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic, state, header, "messagePubkeys", addr)
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

// getIndexCurrentNeedRecovering returns an array of DKG index that needs recover.
func getIndexCurrentNeedRecovering(backend *ethapi.Backend, state *state.StateDB, header *types.Header) ([]uint64, error) {
	var result []*big.Int
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic, state, header, "indexCurrentNeedRecovering")
	if err != nil {
		return nil, err
	}
	var indexs []uint64
	for _, item := range result {
		indexs = append(indexs, item.Uint64())
	}
	return indexs, nil
}

// isShareReady checks if the DKG sharing is 100% uploaded.
func isShareReady(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (bool, error) {
	var result bool
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic, state, header, "isShareReady")
	if err != nil {
		return false, err
	}
	return result, nil
}

// getReshareMsgs gets the reshare messages from specific sender index and round.
func getReshareMsgs(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "getReshareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getResharePVSS gets the reshare PVSS from specific sender index and round.
func getResharePVSS(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "rpvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getShareMsgs gets the share messages from specific sender index and round.
func getShareMsgs(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([][]byte, error) {
	var result [][]byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "getShareMsgs", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getSharePVSS gets the share PVSS from specific sender index and round.
func getSharePVSS(backend *ethapi.Backend, round, index uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "spvsses", big.NewInt(int64(round)), big.NewInt(int64(index)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getRecoverMsgs gets the recover messages from specific sender index and round, with a receiver index.
func getRecoverMsgs(backend *ethapi.Backend, round, senderIndex, arrIndex uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "recoverMsgs", big.NewInt(int64(round)), big.NewInt(int64(senderIndex)), big.NewInt(int64(arrIndex)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getAggregatedCommitment gets the global aggregated commitment after DKG share.
func getAggregatedCommitment(backend *ethapi.Backend, round uint64, state *state.StateDB, header *types.Header) ([]byte, error) {
	var result []byte
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "aggregatedCommitments", big.NewInt(int64(round)))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getRoundNumber gets the DKG round number.
func getRoundNumber(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "roundNumber")
	if err != nil {
		return 0, err
	}
	return result.Uint64(), nil
}

// getZKVersion gets the DKG ZK version
func getZKVersion(backend *ethapi.Backend, state *state.StateDB, header *types.Header) (uint64, error) {
	var result *big.Int
	err := readFromContract(&result, backend, systemcontracts.KeyManagementProxyHash, systemcontracts.KeyManagementABIBasic,
		state, header, "ZK_VERSION")
	if err != nil {
		if strings.Contains(err.Error(), "abi: attempting to unmarshal an empty string while arguments are expected") {
			// Old KeyManagement version doesn't contain ZK_VERSION method in fact, so treat this error as zero version.
			return 0, nil
		}
		return 0, err
	}
	return result.Uint64(), nil
}

// readFromContract calls a contract with ABI-packed inputs.
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

// sendTransactionToKeyManagement sends a transaction to KeyManagement contract.
func sendTransactionToKeyManagement(api *ethapi.TransactionAPI, signer common.Address, method string, zkVersion uint64, args ...interface{}) (*common.Hash, error) {
	if api == nil {
		return nil, errors.New("eth transaction API is not initialized, DKG can't function properly")
	}
	// Choose different abi depends on the ZK settings.
	var abi abi.ABI
	switch zkVersion {
	case 0:
		abi = systemcontracts.KeyManagementABIZKV0
	case 1:
		abi = systemcontracts.KeyManagementABIZKV1
	default:
		panic(fmt.Errorf("unknown ZK version %d", zkVersion))
	}
	data, err := abi.Pack(method, args...)
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

// encodeMessages encodes the output from message encryption.
func encodeMessages(encryptedFis [][]byte, bigRs []*secp256k1.G1Affine, nonces [][]byte) [][]byte {
	result := make([][]byte, 0)
	for i := range encryptedFis {
		bigRBytes := bigRs[i].RawBytes()
		prefix := append(bigRBytes[:], nonces[i]...)
		result = append(result, append(prefix, encryptedFis[i]...))
	}
	return result
}
