package antimev

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/google/uuid"
	keystorev4 "github.com/wealdtech/go-eth2-wallet-encryptor-keystorev4"
)

var (
	ErrInvalidLength     = errors.New("invalid array length")
	ErrInvalidThreshold  = errors.New("invalid threshold")
	ErrInvalidSender     = errors.New("invalid message sender")
	ErrInvalidDKGPVSS    = errors.New("invalid dkg pvss")
	ErrNotParticipant    = errors.New("not the role to send or receive")
	ErrKeyGroupNotExists = errors.New("required keygroup not found")
	ErrNoNeedToRecover   = errors.New("no need to recover")
	ErrUnrecoverable     = errors.New("unrecoverable sharing")
	ErrMessageDecryption = errors.New("message decryption failed")
)

// KeyStore is the container of all useful dkg information.
type KeyStore struct {
	size      int // The size of each key group
	threshold int // The threshold of each key group
	scaler    int // The scaler to speed up computation, refer to crypto/tpke

	address   common.Address    // Self account address
	ethPrvKey *ecies.PrivateKey // Self account secret key
	path      string            // The local storage path
	password  string            // The password for storage encryption

	recovering *thresholdKeyGroup // The recovering key group
	resharing  *thresholdKeyGroup // The resharing key group
	reshared   *thresholdKeyGroup // The group can decrypt old messages
	sharing    *thresholdKeyGroup // The sharing key group
	shared     *thresholdKeyGroup // The group can encrypt and decrypt new messages

	mu sync.RWMutex // Mutex protecting the file persistence
}

// NewKeyStore returns a new instance of antimev keystore.
func NewKeyStore(path string) *KeyStore {
	return &KeyStore{
		path: path,
	}
}

// Init initializes necessary fields of an antimev keystore for dkg.
func (ks *KeyStore) Init(addr common.Address, prvkey *ecies.PrivateKey, groupSize int, threshold int, password string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if groupSize < threshold {
		return ErrInvalidThreshold
	}
	ks.size = groupSize
	ks.threshold = threshold
	ks.scaler = getScaler(groupSize, threshold)
	ks.address = addr
	ks.ethPrvKey = prvkey
	ks.password = password
	return ks.saveStoreAndReInitialize()
}

// Load loads hex-encoded anti-MEV keystore from the provided filepath.
func (ks *KeyStore) Load(password string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.password = password
	return ks.initializeKeystoreFromFile()
}

// Update changes the passphrase of anti-MEV keystore
func (ks *KeyStore) Update(password string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.password = password
	return ks.saveStoreAndReInitialize()
}

// Address returns the keystore specified participant address in DKG
func (ks *KeyStore) Address() common.Address {
	return ks.address
}

// Path returns the file path of keystore storage
func (ks *KeyStore) Path() (string, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return filepath.Abs(ks.path)
}

// MessagePubKey returns a hex string of message encryption key
func (ks *KeyStore) MessagePubKey() string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return hex.EncodeToString(crypto.FromECDSAPub(&ks.ethPrvKey.ExportECDSA().PublicKey))
}

// IsResharing returns if there is an ongoing resharing
func (ks *KeyStore) IsResharing() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.resharing != nil
}

// IsSharing returns if there is an ongoing sharing
func (ks *KeyStore) IsSharing() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.sharing != nil
}

// IsRecovering returns if there is an ongoing recovering
func (ks *KeyStore) IsRecovering() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.recovering != nil
}

// HasReshared returns if there is a completed resharing
func (ks *KeyStore) HasReshared() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.reshared != nil
}

// HasShared returns if there is a completed sharing
func (ks *KeyStore) HasShared() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.shared != nil
}

// OnValidatorList initializes sharing and resharing, should be called
// when the key group members are determined. It initializes 1 or 2 key groups.
func (ks *KeyStore) OnValidatorList(validators []common.Address, pubkeys []*ecies.PublicKey) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if len(validators) != ks.size || len(pubkeys) != ks.size {
		return ErrInvalidLength
	}
	// Set up groups for dkg resharing
	if ks.shared != nil {
		ks.resharing = ks.shared.newTemplateForReshare(validators, pubkeys)
	}
	// Set groups for dkg sharing
	ks.sharing = newThresholdKeyGroup(validators, pubkeys)
	// Remove recovering just in case
	ks.recovering = nil
	return ks.saveStoreAndReInitialize()
}

// OnRecoverPeriodStart initializes recovering, should be called after resharing
// gets finished. It initializes 1 key groups.
func (ks *KeyStore) OnRecoverPeriodStart(indexes []int, validators []common.Address, pubkeys []*ecies.PublicKey) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.shared == nil {
		return ErrKeyGroupNotExists
	}
	if len(validators) != len(indexes) || len(pubkeys) != len(indexes) {
		return ErrInvalidLength
	}
	// Return error if amount is 0
	if len(indexes) < 1 {
		return ErrNoNeedToRecover
	}
	// Return error if amount is more than recoverable
	if len(indexes) > ks.size-ks.threshold {
		return ErrUnrecoverable
	}
	// Only place new ones in recovering group
	ks.recovering = ks.shared.newTemplateForRecover(indexes, validators, pubkeys)
	return ks.saveStoreAndReInitialize()
}

// DKGReshare generates and returns resharing messages and pvss.
func (ks *KeyStore) DKGReshare() ([][]byte, []byte, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	if ks.resharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	if ks.shared != nil {
		if ks.shared.holderIndex(ks.address) < 1 {
			return nil, nil, ErrNotParticipant
		}
		// Generate secret resharing messages and pvss
		rMsgs, rPvss, err := ks.shared.reshare(ks.resharing)
		if err != nil {
			return nil, nil, err
		}
		return rMsgs, rPvss.Encode(), nil
	}
	return nil, nil, nil
}

// DKGShare generates and returns sharing messages and pvss.
func (ks *KeyStore) DKGShare() ([][]byte, []byte, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.sharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	if ks.sharing.holderIndex(ks.address) < 1 {
		return nil, nil, ErrNotParticipant
	}
	// Generate secret sharing messages and pvss
	sMsgs, sPvss, err := ks.sharing.prepare(ks.threshold)
	if err != nil || sPvss == nil {
		return nil, nil, err
	}
	return sMsgs, sPvss.Encode(), nil
}

// DKGRecover generates and returns recovering messages and pvss.
// It will returns nil if no need to recover, or an error if not recoverable.
func (ks *KeyStore) DKGRecover() ([][]byte, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	if ks.shared == nil || ks.recovering == nil {
		return nil, ErrKeyGroupNotExists
	}
	// Generate secret recovering messages
	return ks.shared.recover(ks.recovering.holderIndexesAndKeys())
}

// TryRecoverReshare tries to recover a resharing message, should be called when
// resharing fails and receives a recover message. It returns resharing messages
// immediately if recoverd, otherwise an error.
func (ks *KeyStore) TryRecoverReshare() ([][]byte, []byte, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.recovering == nil || ks.resharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	msgs, pvss, err := ks.recovering.reshareRecovered(ks.threshold, ks.resharing)
	if err != nil || pvss == nil {
		return nil, nil, err
	}
	// Store only if some secret is recovered
	err = ks.saveStoreAndReInitialize()
	if err != nil {
		return nil, nil, err
	}
	return msgs, pvss.Encode(), nil
}

// OnEpochChange checks and settles the results of sharing and resharing, will
// revert both of them of any of them is not successful.
// This method should only be called in the end of dkg, otherwise use
// AggregateShare to check if sharing is successful.
func (ks *KeyStore) OnEpochChange(aggregatedCmt []byte) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Revert dkg if contract doesn't give a aggregated commitment
	if len(aggregatedCmt) == 0 {
		ks.resharing = nil
		ks.sharing = nil
		ks.recovering = nil
		return ks.saveStoreAndReInitialize()
	}
	// If code reaches here, then dkg is successful in contract
	if err := ks.aggregateReshare(); err != nil {
		return err
	}
	if err := ks.aggregateShare(aggregatedCmt); err != nil {
		return err
	}
	// Set finished dkg as current using
	ks.reshared = ks.resharing
	ks.resharing = nil
	ks.shared = ks.sharing
	ks.sharing = nil
	ks.recovering = nil
	return ks.saveStoreAndReInitialize()
}

// aggregateShare tries to check if sharing is successful and settle the result
func (ks *KeyStore) aggregateShare(aggregatedCmt []byte) error {
	// Check if sharing is successful and aggregate keys
	if ks.sharing == nil {
		return ErrKeyGroupNotExists
	}
	selfIndex := ks.sharing.holderIndex(ks.address)
	return ks.sharing.aggregate(ks.scaler, aggregatedCmt, selfIndex)
}

// aggregateReshare tries to check if resharing is successful and settle the result
func (ks *KeyStore) aggregateReshare() error {
	// Check if resharing is successful and aggregate keys
	if ks.resharing != nil {
		selfIndex := ks.resharing.holderIndex(ks.address)
		return ks.resharing.aggregate(ks.scaler, nil, selfIndex)
	}
	return nil
}

// ReceiveSecretShare tries to verify a sharing message array and store their data.
func (ks *KeyStore) ReceiveSecretShare(from common.Address, ess [][]byte, pvss []byte) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Check if out of the period
	if ks.sharing == nil {
		return ErrKeyGroupNotExists
	}
	// If is a member of receiver
	selfIndex := ks.sharing.holderIndex(ks.address)
	if selfIndex < 1 {
		return ErrNotParticipant
	}
	// Check sender's identity
	fromIndex := ks.sharing.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrInvalidSender
	}
	// Check if the secret share has a valid length
	if len(ess) < ks.size {
		return ErrDKGSecret
	}
	// Decode PVSS
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrInvalidDKGPVSS
	}
	// Decrypt and accept the sharing message
	ss, err := ks.decryptSecretShare(ess[selfIndex-1])
	if err != nil {
		return ErrDecryptionFailed
	}
	err = ks.sharing.receiveShareMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

// ReceiveSecretReshare tries to verify a resharing message array and store their data.
func (ks *KeyStore) ReceiveSecretReshare(from common.Address, ers [][]byte, pvss []byte) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Check if out of the period
	if ks.shared == nil || ks.resharing == nil {
		return ErrKeyGroupNotExists
	}
	// If is a member of receiver
	selfIndex := ks.resharing.holderIndex(ks.address)
	if selfIndex < 1 {
		return ErrNotParticipant
	}
	// Check sender's identity
	fromIndex := ks.shared.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrInvalidSender
	}
	// Check if the secret share has a valid length
	if len(ers) < ks.size {
		return ErrDKGSecret
	}
	// Decode PVSS
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrInvalidDKGPVSS
	}
	// Decrypt and accept the resharing message
	ss, err := ks.decryptSecretShare(ers[selfIndex-1])
	if err != nil {
		return ErrDecryptionFailed
	}
	err = ks.resharing.receiveReshareMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

// ReceiveRecoveredReshare tries to verify a resharing message array and store their data.
func (ks *KeyStore) ReceiveRecoveredReshare(from common.Address, ers [][]byte, pvss []byte) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Check if out of the period
	if ks.recovering == nil || ks.resharing == nil {
		return ErrKeyGroupNotExists
	}
	// If is a member of receiver
	selfIndex := ks.resharing.holderIndex(ks.address)
	if selfIndex < 1 {
		return ErrNotParticipant
	}
	// Check sender's identity
	fromIndex := ks.recovering.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrInvalidSender
	}
	// Check if the secret share has a valid length
	if len(ers) < ks.size {
		return ErrDKGSecret
	}
	// Decode PVSS
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrInvalidDKGPVSS
	}
	// Decrypt and accept the recoverd resharing message
	ss, err := ks.decryptSecretShare(ers[selfIndex-1])
	if err != nil {
		return ErrDecryptionFailed
	}
	err = ks.resharing.receiveReshareMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

// ReceiveRecoverShare tries to verify a recovering message array and store their data.
func (ks *KeyStore) ReceiveRecoverShare(from common.Address, ers []byte, pvss []byte) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	// Check if out of the period
	if ks.shared == nil || ks.recovering == nil {
		return ErrKeyGroupNotExists
	}
	// If is a member of receiver
	selfIndex := ks.recovering.holderIndex(ks.address)
	if selfIndex < 1 {
		return ErrNotParticipant
	}
	// Decode PVSS
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrInvalidDKGPVSS
	}
	// Check sender's identity
	fromIndex := ks.shared.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrInvalidSender
	}
	// Decrypt and accept the recovering message
	ss, err := ks.decryptSecretShare(ers)
	if err != nil {
		return ErrDecryptionFailed
	}
	err = ks.recovering.receiveRecoverMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

func (ks *KeyStore) decryptSecretShare(ess []byte) (*big.Int, error) {
	ss, err := ks.ethPrvKey.Decrypt(ess, nil, nil)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(ss), nil
}

// keyStoreAux is an auxiliary structure for KeyStore JSON marshalling.
type keyStoreAux struct {
	Size      int `json:"size"`      // The size of each key group
	Threshold int `json:"threshold"` // The threshold of each key group
	Scaler    int `json:"scaler"`    // The scaler to speed up computation, refer to crypto/tpke

	Address   common.Address `json:"address"`     // Self account address
	EthPrvKey string         `json:"eth_prv_key"` // Self account secret key

	Recovering *thresholdKeyGroupAux `json:"recovering"` // The recovering key group
	Resharing  *thresholdKeyGroupAux `json:"resharing"`  // The resharing key group
	Reshared   *thresholdKeyGroupAux `json:"reshared"`   // The group can decrypt old messages
	Sharing    *thresholdKeyGroupAux `json:"sharing"`    // The sharing key group
	Shared     *thresholdKeyGroupAux `json:"shared"`     // The group can encrypt and decrypt new messages
}

// keystoreRepresentation defines an internal representation of amev secrets,
// encrypted according to the EIP-2334 standard.
type keystoreRepresentation struct {
	Crypto  map[string]interface{} `json:"crypto"`
	ID      string                 `json:"uuid"`
	Version uint                   `json:"version"`
	Name    string                 `json:"name"`
}

// toAux transforms KeyStore to JSON serializable structure.
// Absent thresholdKeyGroups remain nil, but their positions are allocated.
func (ks *KeyStore) toAux() *keyStoreAux {
	aux := &keyStoreAux{
		Size:      ks.size,
		Threshold: ks.threshold,
		Scaler:    ks.scaler,

		Address:   ks.address,
		EthPrvKey: hex.EncodeToString(crypto.FromECDSA(ks.ethPrvKey.ExportECDSA())),
	}

	if ks.recovering != nil {
		aux.Recovering = ks.recovering.toAux()
	}
	if ks.resharing != nil {
		aux.Resharing = ks.resharing.toAux()
	}
	if ks.reshared != nil {
		aux.Reshared = ks.reshared.toAux()
	}
	if ks.sharing != nil {
		aux.Sharing = ks.sharing.toAux()
	}
	if ks.shared != nil {
		aux.Shared = ks.shared.toAux()
	}
	return aux
}

// fromAux transforms deserialized JSON data to KeyStore.
func (ks *KeyStore) fromAux(aux *keyStoreAux) error {
	ks.size = aux.Size
	ks.threshold = aux.Threshold
	ks.scaler = aux.Scaler

	ks.address = aux.Address
	keyBytes, err := hex.DecodeString(aux.EthPrvKey)
	if err != nil {
		return err
	}
	privKey, err := crypto.ToECDSA(keyBytes)
	if err != nil {
		return err
	}
	ks.ethPrvKey = ecies.ImportECDSA(privKey)

	if aux.Recovering != nil {
		ks.recovering = new(thresholdKeyGroup)
		err = ks.recovering.fromAux(aux.Recovering)
		if err != nil {
			return err
		}
	} else {
		ks.recovering = nil
	}
	if aux.Resharing != nil {
		ks.resharing = new(thresholdKeyGroup)
		err = ks.resharing.fromAux(aux.Resharing)
		if err != nil {
			return err
		}
	} else {
		ks.resharing = nil
	}
	if aux.Reshared != nil {
		ks.reshared = new(thresholdKeyGroup)
		err = ks.reshared.fromAux(aux.Reshared)
		if err != nil {
			return err
		}
	} else {
		ks.reshared = nil
	}
	if aux.Sharing != nil {
		ks.sharing = new(thresholdKeyGroup)
		err = ks.sharing.fromAux(aux.Sharing)
		if err != nil {
			return err
		}
	} else {
		ks.sharing = nil
	}
	if aux.Shared != nil {
		ks.shared = new(thresholdKeyGroup)
		err = ks.shared.fromAux(aux.Shared)
		if err != nil {
			return err
		}
	} else {
		ks.shared = nil
	}

	return nil
}

var (
	ErrKeystoreEncryption = errors.New("could not encrypt keystore")
	ErrKeystoreDecryption = errors.New("could not decrypt keystore")
)

// initializeKeystoreFromFile loads an existing keystore from file.
func (ks *KeyStore) initializeKeystoreFromFile() error {
	encoded, err := readFileAtPath(filepath.Dir(ks.path), filepath.Base(ks.path))
	if err != nil {
		return err
	}
	keystoreFile := &keystoreRepresentation{}
	if err := json.Unmarshal(encoded, keystoreFile); err != nil {
		return err
	}
	// Extract the validator signing private key from the keystore
	decryptor := keystorev4.New()
	enc, err := decryptor.Decrypt(keystoreFile.Crypto, ks.password)
	if err != nil {
		return ErrKeystoreDecryption
	}
	store := &keyStoreAux{}
	if err := json.Unmarshal(enc, store); err != nil {
		return err
	}
	return ks.fromAux(store)
}

// saveStoreAndReInitialize saves the keystore to disk and re-initializes the keystore from file.
func (ks *KeyStore) saveStoreAndReInitialize() error {
	// Save the copy to disk
	keystoreRepr, err := createKeystoreRepresentation(ks.toAux(), ks.password)
	if err != nil {
		return err
	}
	encodedData, err := json.MarshalIndent(keystoreRepr, "", "\t")
	if err != nil {
		return err
	}
	err = writeFileAtPath(filepath.Dir(ks.path), filepath.Base(ks.path), encodedData)
	if err != nil {
		return err
	}
	// ReInitialize and update the memory
	err = ks.initializeKeystoreFromFile()
	return err
}

// createKeystoreRepresentation is a pure function that takes a keystore and password and returns the encrypted formatted json version for file writing.
func createKeystoreRepresentation(store *keyStoreAux, password string) (*keystoreRepresentation, error) {
	encryptor := keystorev4.New()
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	encodedStore, err := json.MarshalIndent(store, "", "\t")
	if err != nil {
		return nil, err
	}
	cryptoFields, err := encryptor.Encrypt(encodedStore, password)
	if err != nil {
		return nil, ErrKeystoreEncryption
	}
	return &keystoreRepresentation{
		Crypto:  cryptoFields,
		ID:      id.String(),
		Version: encryptor.Version(),
		Name:    encryptor.Name(),
	}, nil
}
