package antimev

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"

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
	ErrNotParticipant    = errors.New("not dkg participant")
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
	return ks.saveStoreAndReInitialize()
}

// Load loads hex-encoded anti-MEV keystore from the provided filepath.
func (ks *KeyStore) Load(password string) error {
	ks.password = password
	return ks.initializeKeystoreFromFile()
}

// OnValidatorList initializes sharing and resharing, should be called
// when the key group members are determined. It returns resharing
// messages immediately.
func (ks *KeyStore) OnValidatorList(validators []common.Address, pubkeys []*ecies.PublicKey) ([][]byte, []byte, error) {
	if len(validators) != ks.size || len(pubkeys) != ks.size {
		return nil, nil, ErrInvalidLength
	}
	// Set up groups for dkg sharing and resharing
	if ks.shared != nil {
		ks.resharing = ks.shared.newTemplateForReshare(validators, pubkeys)
	}
	ks.sharing = newThresholdKeyGroup(ks.address, validators, pubkeys)
	// Resharing doesn't require persistence, so store here
	err := ks.saveStoreAndReInitialize()
	if err != nil {
		return nil, nil, err
	}
	// Generate secret resharing messages and pvss
	if ks.resharing != nil {
		rMsgs, pvss, err := ks.shared.dkgReshare(ks.resharing)
		if err != nil || pvss == nil {
			return nil, nil, err
		}
		return rMsgs, pvss.Encode(), nil
	}
	// Nothing to reshare
	return nil, nil, nil
}

func (ks *KeyStore) OnRecoverStart(indexes []int, validators []common.Address, pubkeys []*ecies.PublicKey) ([][]byte, error) {
	if ks.shared == nil {
		return nil, nil
	}
	if len(validators) != len(indexes) || len(pubkeys) != len(indexes) {
		return nil, ErrInvalidLength
	}
	// Return error if amount is more than recoverable
	if len(indexes) > ks.size-ks.threshold {
		return nil, ErrUnrecoverable
	}
	// Do nothing if no need to recover
	if len(indexes) < 1 {
		return nil, nil
	}
	// Only place new ones in recovering group
	ks.recovering = ks.shared.newTemplateForRecover(indexes, validators, pubkeys)
	// Recovering doesn't require persistence, so store here
	err := ks.saveStoreAndReInitialize()
	if err != nil {
		return nil, err
	}
	// Generate secret recovering messages
	return ks.shared.dkgRecover(indexes, pubkeys)
}

// OnRecoverFinish tries to finish ongoing recovering, should be called when
// resharing fails. It returns resharing messages immediately if recoverd.
func (ks *KeyStore) OnRecoverFinish() ([][]byte, []byte, error) {
	msgs, pvss, err := ks.recovering.dkgReshareRecovered(ks.threshold, ks.resharing)
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

// OnReshareFinish tries to finish ongoing resharing, should be called before
// the new round sharing. It returns sharing messages immediately.
func (ks *KeyStore) OnReshareFinish() ([][]byte, []byte, error) {
	// Check if resharing are finished and aggregate keys
	if ks.resharing != nil {
		err := ks.resharing.dkgAggregate(ks.scaler)
		if err != nil {
			return nil, nil, err
		}
	}
	// Generate secret sharing messages and pvss
	sMsgs, pvss, err := ks.sharing.dkgPrepare(ks.threshold)
	if err != nil || pvss == nil {
		return nil, nil, err
	}
	// Store only if some secret is generated
	err = ks.saveStoreAndReInitialize()
	if err != nil {
		return nil, nil, err
	}
	return sMsgs, pvss.Encode(), nil
}

// OnEpochChange tries to finish ongoing sharing, should be called before
// the key group is needed for encryption and signing.
func (ks *KeyStore) OnEpochChange() error {
	// Check if sharing are finished and aggregate keys
	if ks.sharing != nil {
		err := ks.sharing.dkgAggregate(ks.scaler)
		if err != nil {
			return err
		}
	}
	// Set finished dkg as current using
	ks.reshared = ks.resharing
	ks.resharing = nil
	ks.shared = ks.sharing
	ks.sharing = nil

	return ks.saveStoreAndReInitialize()
}

// ReceiveSecretShare tries to verify a sharing message array and store their data.
func (ks *KeyStore) ReceiveSecretShare(from common.Address, ess [][]byte, pvss []byte) error {
	fromIndex := ks.sharing.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrDKGPVSS
	}
	// If is a member of receiver
	selfIndex := ks.sharing.holderIndex(ks.address)
	if selfIndex > 0 {
		ss, err := ks.decryptSecretShare(ess[selfIndex-1])
		if err != nil {
			return ErrDecryptionFailed
		}
		err = ks.sharing.receiveShareMessage(fromIndex, ss, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	} else {
		err = ks.sharing.receiveShareMessage(fromIndex, nil, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	}
}

// ReceiveSecretReshare tries to verify a resharing message array and store their data.
func (ks *KeyStore) ReceiveSecretReshare(from common.Address, ers [][]byte, pvss []byte) error {
	fromIndex := ks.shared.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrDKGPVSS
	}
	// If is a member of receiver
	selfIndex := ks.resharing.holderIndex(ks.address)
	if selfIndex > 0 {
		ss, err := ks.decryptSecretShare(ers[selfIndex-1])
		if err != nil {
			return ErrDecryptionFailed
		}
		err = ks.resharing.receiveReshareMessage(fromIndex, ss, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	} else {
		err = ks.resharing.receiveReshareMessage(fromIndex, nil, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	}
}

// ReceiveRecoveredReshare tries to verify a resharing message array and store their data.
func (ks *KeyStore) ReceiveRecoveredReshare(from common.Address, ers [][]byte, pvss []byte) error {
	fromIndex := ks.recovering.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).Decode(pvss, ks.size, ks.threshold)
	if err != nil {
		return ErrDKGPVSS
	}
	// If is a member of receiver
	selfIndex := ks.resharing.holderIndex(ks.address)
	if selfIndex > 0 {
		ss, err := ks.decryptSecretShare(ers[selfIndex-1])
		if err != nil {
			return ErrDecryptionFailed
		}
		err = ks.resharing.receiveReshareMessage(fromIndex, ss, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	} else {
		err = ks.resharing.receiveReshareMessage(fromIndex, nil, p)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	}
}

// ReceiveRecoverShare tries to verify a recovering message array and store their data.
func (ks *KeyStore) ReceiveRecoverShare(from common.Address, ers []byte) error {
	fromIndex := ks.shared.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	// If is a member of receiver
	selfIndex := ks.recovering.holderIndex(ks.address)
	if selfIndex > 0 {
		ss, err := ks.decryptSecretShare(ers)
		if err != nil {
			return ErrDecryptionFailed
		}
		err = ks.recovering.receiveRecoverMessage(fromIndex, ss)
		if err != nil {
			return err
		}
		return ks.saveStoreAndReInitialize()
	}
	return nil
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
		err = ks.recovering.fromAux(aux.Recovering, ks.size, ks.threshold)
		if err != nil {
			return err
		}
	} else {
		ks.recovering = nil
	}
	if aux.Resharing != nil {
		ks.resharing = new(thresholdKeyGroup)
		err = ks.resharing.fromAux(aux.Resharing, ks.size, ks.threshold)
		if err != nil {
			return err
		}
	} else {
		ks.resharing = nil
	}
	if aux.Reshared != nil {
		ks.reshared = new(thresholdKeyGroup)
		err = ks.reshared.fromAux(aux.Reshared, ks.size, ks.threshold)
		if err != nil {
			return err
		}
	} else {
		ks.reshared = nil
	}
	if aux.Sharing != nil {
		ks.sharing = new(thresholdKeyGroup)
		err = ks.sharing.fromAux(aux.Sharing, ks.size, ks.threshold)
		if err != nil {
			return err
		}
	} else {
		ks.sharing = nil
	}
	if aux.Shared != nil {
		ks.shared = new(thresholdKeyGroup)
		err = ks.shared.fromAux(aux.Shared, ks.size, ks.threshold)
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
