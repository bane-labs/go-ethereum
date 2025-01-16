package antimev

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"

	"github.com/bane-labs/zk-dkg/encryption"
	"github.com/consensys/gnark-crypto/ecc/secp256k1"
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
	ErrInvalidDKGPVSS    = errors.New("invalid dkg pvss")
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

	round      int                // The index of latest shared and current using key group
	recovering *thresholdKeyGroup // The recovering key group
	resharing  *thresholdKeyGroup // The resharing key group
	reshared   *thresholdKeyGroup // The group can decrypt old messages
	sharing    *thresholdKeyGroup // The sharing key group
	shared     *thresholdKeyGroup // The group can encrypt and decrypt new messages
}

// NewKeyStore returns a new instance of antimev keystore.
func NewKeyStore(path string) *KeyStore {
	return &KeyStore{
		path: path,
	}
}

// Init initializes necessary fields of an antimev keystore for dkg.
func (ks *KeyStore) Init(addr common.Address, prvkey *ecies.PrivateKey, groupSize int, threshold int, password string) error {
	if groupSize < threshold {
		return ErrInvalidThreshold
	}
	ks.size = groupSize
	ks.threshold = threshold
	ks.scaler = getScaler(groupSize, threshold)
	ks.address = addr
	ks.ethPrvKey = prvkey
	ks.password = password
	ks.round = 0
	return ks.saveStoreAndReInitialize()
}

// Reset cleans all dkg progress data and returns to an initial state
func (ks *KeyStore) Reset(round int) error {
	ks.round = round
	ks.recovering = nil
	ks.resharing = nil
	ks.reshared = nil
	ks.sharing = nil
	ks.shared = nil
	return ks.saveStoreAndReInitialize()
}

// Load loads hex-encoded anti-MEV keystore from the provided filepath.
func (ks *KeyStore) Load(password string) error {
	ks.password = password
	return ks.initializeKeystoreFromFile()
}

// Copy creates a deep copy of KeyStore.
func (ks *KeyStore) Copy() *KeyStore {
	cp := *ks
	res := &cp
	if ks.ethPrvKey != nil {
		k := *ks.ethPrvKey
		res.ethPrvKey = &k
	}
	if ks.recovering != nil {
		res.recovering = ks.recovering.copy()
	}
	if ks.resharing != nil {
		res.resharing = ks.resharing.copy()
	}
	if ks.reshared != nil {
		res.reshared = ks.reshared.copy()
	}
	if ks.sharing != nil {
		res.sharing = ks.sharing.copy()
	}
	if ks.shared != nil {
		res.shared = ks.shared.copy()
	}
	return res
}

// Update changes the passphrase of anti-MEV keystore
func (ks *KeyStore) Update(password string) error {
	ks.password = password
	return ks.saveStoreAndReInitialize()
}

// Address returns the keystore specified participant address in DKG
func (ks *KeyStore) Address() common.Address {
	return ks.address
}

// Path returns the file path of keystore storage
func (ks *KeyStore) Path() (string, error) {
	return filepath.Abs(ks.path)
}

// GlobalPublicKey returns global public key that may be used to verify threshold
// signature against. Do not modify the return value.
func (ks *KeyStore) GlobalPublicKey() (*tpke.PublicKey, error) {
	if ks.shared == nil {
		return nil, ErrNoPubKey
	}
	return ks.shared.globalPubKey, nil
}

// MessagePubKey returns a hex string of message encryption key
func (ks *KeyStore) MessagePubKey() string {
	return hex.EncodeToString(crypto.FromECDSAPub(&ks.ethPrvKey.ExportECDSA().PublicKey))
}

// Round returns the index of latest and successful dkg round (not the ongoing one)
func (ks *KeyStore) Round() int {
	return ks.round
}

// CurrentGlobalPubKey returns global public key that may be used to verify threshold
// signature against. Do not modify the return value.
func (ks *KeyStore) CurrentGlobalPubKey() (*tpke.PublicKey, error) {
	if ks.shared == nil {
		return nil, ErrNoPubKey
	}
	return ks.shared.globalPubKey, nil
}

// LastGlobalPubKey returns last round global public key.
func (ks *KeyStore) LastGlobalPubKey() (*tpke.PublicKey, error) {
	if ks.reshared == nil {
		return nil, ErrNoPubKey
	}
	return ks.reshared.globalPubKey, nil
}

// IsResharing returns if there is an ongoing resharing
func (ks *KeyStore) IsResharing() bool {
	return ks.resharing != nil
}

// IsSharing returns if there is an ongoing sharing
func (ks *KeyStore) IsSharing() bool {
	return ks.sharing != nil
}

// IsRecovering returns if there is an ongoing recovering
func (ks *KeyStore) IsRecovering() bool {
	return ks.recovering != nil
}

// HasReshared returns if there is a completed resharing
func (ks *KeyStore) HasReshared() bool {
	return ks.reshared != nil
}

// HasShared returns if there is a completed sharing
func (ks *KeyStore) HasShared() bool {
	return ks.shared != nil
}

// OnValidatorList initializes sharing and resharing, should be called
// when the key group members are determined. It initializes 1 or 2 key groups.
func (ks *KeyStore) OnSharePeriodStart() error {
	// Set up groups for dkg resharing
	if ks.shared != nil {
		ks.resharing = ks.shared.newTemplateForReshare(ks.size)
	}
	// Set groups for dkg sharing
	ks.sharing = newThresholdKeyGroup(ks.size)
	// Remove recovering just in case
	ks.recovering = nil
	return ks.saveStoreAndReInitialize()
}

// OnRecoverPeriodStart initializes recovering, should be called after resharing
// gets finished. It initializes 1 key groups.
func (ks *KeyStore) OnRecoverPeriodStart() error {
	if ks.shared == nil {
		return ErrKeyGroupNotExists
	}
	ks.recovering = newThresholdKeyGroup(ks.size)
	return ks.saveStoreAndReInitialize()
}

// DKGReshare generates and returns resharing messages and pvss. The input key
// array should be ordered by DKG index, e.g. keystore will use 1 as the index
// to generate a reshare message for messagePubKeys[0].
func (ks *KeyStore) DKGReshare(messagePubKeys []*ecies.PublicKey) ([][]byte, []byte, error) {
	if ks.resharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	if len(messagePubKeys) != ks.size {
		return nil, nil, ErrInvalidLength
	}
	if ks.shared != nil {
		// Generate secret resharing messages and pvss
		rMsgs, rPvss, err := ks.shared.reshare(messagePubKeys)
		if err != nil {
			return nil, nil, err
		}
		// No data changed so no need to persist
		return rMsgs, rPvss.Encode(), nil
	}
	return nil, nil, nil
}

// DKGShare generates and returns sharing messages and pvss. The input key array
// should be ordered by DKG index, e.g. keystore will use 1 as the index to
// generate a share message for messagePubKeys[0].
func (ks *KeyStore) DKGShare(messagePubKeys []*ecies.PublicKey) ([][]byte, []byte, error) {
	if ks.sharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	if len(messagePubKeys) != ks.size {
		return nil, nil, ErrInvalidLength
	}
	// Generate secret sharing messages and pvss
	sMsgs, sPvss, err := ks.sharing.prepare(ks.threshold, messagePubKeys)
	if err != nil || sPvss == nil {
		return nil, nil, err
	}
	// Store only if some secret is generated
	err = ks.saveStoreAndReInitialize()
	if err != nil {
		return nil, nil, err
	}
	return sMsgs, sPvss.Encode(), nil
}

// DKGRecover generates and returns recovering messages and pvss. The input index
// should be a DKG index starts from 1, and the size of two inputs should be the
// same.
func (ks *KeyStore) DKGRecover(indexes []int, messagePubKeys []*ecies.PublicKey) ([][]byte, error) {
	if ks.shared == nil || ks.recovering == nil {
		return nil, ErrKeyGroupNotExists
	}
	if len(messagePubKeys) < 1 {
		return nil, ErrNoNeedToRecover
	}
	if len(messagePubKeys) > ks.size-ks.threshold {
		return nil, ErrUnrecoverable
	}
	// Generate secret recovering messages
	return ks.shared.recover(indexes, messagePubKeys)
}

// TryRecoverReshare tries to recover a resharing message, should be called when
// resharing fails and receives a recover message. It returns resharing messages
// immediately if recoverd, otherwise an error. The input key array should be
// ordered by DKG index, e.g. keystore will use 1 as the index to generate a
// share message for messagePubKeys[0].
func (ks *KeyStore) TryRecoverReshare(messagePubKeys []*ecies.PublicKey) ([][]byte, []byte, error) {
	if ks.recovering == nil || ks.resharing == nil {
		return nil, nil, ErrKeyGroupNotExists
	}
	msgs, pvss, err := ks.recovering.reshareRecovered(ks.threshold, messagePubKeys)
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

// RevertRound reverts all progress of current round of DKG.
func (ks *KeyStore) RevertRound() error {
	ks.resharing = nil
	ks.sharing = nil
	ks.recovering = nil
	return ks.saveStoreAndReInitialize()
}

// OnEpochChange checks and settles the results of sharing and resharing, will
// revert both of them of any of them is not successful.
func (ks *KeyStore) OnEpochChange(selfPvss []byte, aggregatedCmt []byte, isMemberOfNewGroup bool) error {
	// Revert dkg if contract doesn't give a aggregated commitment
	if len(aggregatedCmt) == 0 {
		return ks.RevertRound()
	}
	if isMemberOfNewGroup && len(selfPvss) == 0 {
		return ErrLengthMismatch
	}
	if !isMemberOfNewGroup && len(selfPvss) > 0 {
		return ErrLengthMismatch
	}
	// If code reaches here, then dkg is successful in contract
	if err := ks.aggregateReshare(isMemberOfNewGroup); err != nil {
		return err
	}
	if err := ks.aggregateShare(selfPvss, aggregatedCmt, isMemberOfNewGroup); err != nil {
		return err
	}
	// Set finished dkg as current using
	ks.reshared = ks.resharing
	ks.resharing = nil
	ks.shared = ks.sharing
	ks.sharing = nil
	ks.recovering = nil
	ks.round += 1
	return ks.saveStoreAndReInitialize()
}

// aggregateShare tries to check if sharing is successful and settle the result
func (ks *KeyStore) aggregateShare(selfPvss []byte, aggregatedCmt []byte, isParticipant bool) error {
	// Check if sharing is successful and aggregate keys
	if ks.sharing == nil {
		return ErrKeyGroupNotExists
	}
	// Check which secret is finally confirmed by contract
	if isParticipant {
		p, err := new(tpke.PVSS).Decode(selfPvss, ks.size, ks.threshold)
		if err != nil {
			return ErrInvalidDKGPVSS
		}
		err = ks.sharing.confirmSecret(p)
		if err != nil {
			return err
		}
	}
	return ks.sharing.aggregate(ks.scaler, aggregatedCmt, isParticipant)
}

// aggregateReshare tries to check if resharing is successful and settle the result
func (ks *KeyStore) aggregateReshare(isReceiver bool) error {
	// Check if resharing is successful and aggregate keys
	if ks.resharing == nil {
		return nil
	}
	return ks.resharing.aggregate(ks.scaler, nil, isReceiver)
}

// ReceiveSecretShare tries to verify a sharing message array and store their data.
// Both selfIndex and fromIndex should be a DKG index starts from 1.
func (ks *KeyStore) ReceiveSecretShare(selfIndex int, fromIndex int, ess [][]byte, pvss []byte) error {
	// Check if out of the period
	if ks.sharing == nil {
		return ErrKeyGroupNotExists
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
	ss, err := ks.decryptShareMessage(ess[selfIndex-1])
	if err != nil {
		return ErrMessageDecryption
	}
	err = ks.sharing.receiveShareMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

// ReceiveSecretReshare tries to verify a resharing message array and store their data.
// Both selfIndex and fromIndex should be a DKG index starts from 1.
func (ks *KeyStore) ReceiveSecretReshare(selfIndex int, fromIndex int, ers [][]byte, pvss []byte) error {
	// Check if out of the period
	if ks.shared == nil || ks.resharing == nil {
		return ErrKeyGroupNotExists
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
	ss, err := ks.decryptShareMessage(ers[selfIndex-1])
	if err != nil {
		return ErrMessageDecryption
	}
	err = ks.resharing.receiveReshareMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

// ReceiveRecoverShare tries to verify a recovering message array and store their data.
// Both selfIndex and fromIndex should be a DKG index starts from 1.
func (ks *KeyStore) ReceiveRecoverShare(selfIndex int, fromIndex int, ers []byte, pvss []byte) error {
	// Check if out of the period
	if ks.shared == nil || ks.recovering == nil {
		return ErrKeyGroupNotExists
	}
	// Decode PVSS
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrInvalidDKGPVSS
	}
	// Decrypt and accept the recovering message
	ss, err := ks.decryptShareMessage(ers)
	if err != nil {
		return ErrMessageDecryption
	}
	err = ks.recovering.receiveRecoverMessage(fromIndex, ss, p, selfIndex)
	if err != nil {
		return err
	}
	return ks.saveStoreAndReInitialize()
}

func (ks *KeyStore) decryptShareMessage(msg []byte) (*big.Int, error) {
	// len(message)=12+64+len(ess)
	if len(msg) <= 76 {
		return nil, errors.New("invalid message length")
	}
	var bigR secp256k1.G1Affine
	_, err := bigR.SetBytes(msg[:64])
	if err != nil {
		return nil, err
	}
	ss, err := encryption.ECIESDecrypt(ks.ethPrvKey, msg[76:], msg[64:76], bigR)
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

	Round      int                   `json:"round"`      // The index of latest shared and current using key group
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

	aux.Round = ks.round
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

	ks.round = aux.Round
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

	return ks.initializeKeystoreFromBytes(encoded)
}

func (ks *KeyStore) initializeKeystoreFromBytes(encoded []byte) error {
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
	// A temporary hack to avoid useless keystore file overwrites until persistence logic is
	// implemented over keystore, ref. https://github.com/bane-labs/go-ethereum/issues/388.
	if ks.path != "" {
		err = writeFileAtPath(filepath.Dir(ks.path), filepath.Base(ks.path), encodedData)
		if err != nil {
			return err
		}

		// ReInitialize and update the memory
		err = ks.initializeKeystoreFromFile()
	} else {
		err = ks.initializeKeystoreFromBytes(encodedData)
	}

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
