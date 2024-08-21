package antimev

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	ErrInvalidLength     = errors.New("invalid array length")
	ErrInvalidThreshold  = errors.New("invalid threshold")
	ErrNotParticipant    = errors.New("not dkg participant")
	ErrUnrecoverable     = errors.New("unrecoverable sharing")
	ErrMessageDecryption = errors.New("message decryption failed")
)

// AMEVKeyStore is the container of all useful dkg information.
type AMEVKeyStore struct {
	size      int // The size of each key group
	threshold int // The threshold of each key group
	scaler    int // The scaler to speed up computation, refer to crypto/tpke

	address   common.Address    // Self account address
	ethPrvKey *ecies.PrivateKey // Self account secret key

	recovering *thresholdKeyGroup // The recovering key group
	resharing  *thresholdKeyGroup // The resharing key group
	reshared   *thresholdKeyGroup // The group can decrypt old messages
	shared     *thresholdKeyGroup // The sharing key group
	sharing    *thresholdKeyGroup // The group can encrypt and decrypt new messages
}

// NewKeyStore returns a new instance of antimev key store.
func NewKeyStore(addr common.Address, prvkey *ecies.PrivateKey, groupSize int, threshold int) (*AMEVKeyStore, error) {
	// TODO: Recover keystore from persistence
	if groupSize < threshold {
		return nil, ErrInvalidThreshold
	}
	return &AMEVKeyStore{
		size:      groupSize,
		threshold: threshold,
		scaler:    getScaler(groupSize, threshold),
		address:   addr,
		ethPrvKey: prvkey,
	}, nil
}

// OnValidatorList initializes sharing and resharing, should be called
// when the key group members are determined. It returns resharing
// messages immediately.
func (ks *AMEVKeyStore) OnValidatorList(validators []common.Address, pubkeys []*ecies.PublicKey) ([][]byte, []byte, error) {
	if len(validators) != ks.size || len(pubkeys) != ks.size {
		return nil, nil, ErrInvalidLength
	}
	// Set up groups for dkg sharing and resharing
	if ks.shared != nil {
		ks.resharing = ks.shared.newTemplateForReshare(validators, pubkeys)
	}
	ks.sharing = newThresholdKeyGroup(ks.address, validators, pubkeys)
	// Generate secret resharing messages and pvss
	if ks.resharing != nil {
		rMsgs, pvss, err := ks.shared.dkgReshare(ks.resharing)
		if err != nil || pvss == nil {
			return nil, nil, err
		}
		return rMsgs, pvss.ToBytes(), nil
	}
	// Nothing to reshare
	return nil, nil, nil
}

func (ks *AMEVKeyStore) OnRecoverStart(indexes []int, validators []common.Address, pubkeys []*ecies.PublicKey) ([][]byte, error) {
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
	// Generate secret recovering messages
	return ks.shared.dkgRecover(indexes, pubkeys)
}

// OnRecoverFinish tries to finish ongoing recovering, should be called when
// resharing fails. It returns resharing messages immediately if recoverd.
func (ks *AMEVKeyStore) OnRecoverFinish() ([][]byte, []byte, error) {
	msgs, pvss, err := ks.recovering.dkgReshareRecovered(ks.threshold, ks.resharing)
	if err != nil || pvss == nil {
		return nil, nil, err
	}
	return msgs, pvss.ToBytes(), nil
}

// OnReshareFinish tries to finish ongoing resharing, should be called before
// the new round sharing. It returns sharing messages immediately.
func (ks *AMEVKeyStore) OnReshareFinish() ([][]byte, []byte, error) {
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
	return sMsgs, pvss.ToBytes(), nil
}

// OnEpochChange tries to finish ongoing sharing, should be called before
// the key group is needed for encryption and signing.
func (ks *AMEVKeyStore) OnEpochChange() error {
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

	return nil
}

// ReceiveSecretShare tries to verify a sharing message array and store their data.
func (ks *AMEVKeyStore) ReceiveSecretShare(from common.Address, ess [][]byte, pvss []byte) error {
	fromIndex := ks.sharing.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).FromBytes(pvss, ks.size, ks.threshold)
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
		return ks.sharing.receiveShareMessage(fromIndex, ss, p)
	}
	return ks.sharing.receiveShareMessage(fromIndex, nil, p)
}

// ReceiveSecretReshare tries to verify a resharing message array and store their data.
func (ks *AMEVKeyStore) ReceiveSecretReshare(from common.Address, ers [][]byte, pvss []byte) error {
	fromIndex := ks.shared.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).FromBytes(pvss, ks.size, ks.threshold)
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
		return ks.resharing.receiveReshareMessage(fromIndex, ss, p)
	}
	return ks.resharing.receiveReshareMessage(fromIndex, nil, p)
}

// ReceiveRecoveredReshare tries to verify a resharing message array and store their data.
func (ks *AMEVKeyStore) ReceiveRecoveredReshare(from common.Address, ers [][]byte, pvss []byte) error {
	fromIndex := ks.recovering.holderIndex(from)
	if fromIndex > ks.size || fromIndex < 1 {
		return ErrNotParticipant
	}
	p, err := new(tpke.PVSS).FromBytes(pvss, ks.size, ks.threshold)
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
		return ks.resharing.receiveReshareMessage(fromIndex, ss, p)
	}
	return ks.resharing.receiveReshareMessage(fromIndex, nil, p)
}

// ReceiveRecoverShare tries to verify a recovering message array and store their data.
func (ks *AMEVKeyStore) ReceiveRecoverShare(from common.Address, ers []byte) error {
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
		return ks.recovering.receiveRecoverMessage(fromIndex, ss)
	}
	return nil
}

func (ks *AMEVKeyStore) decryptSecretShare(ess []byte) (*big.Int, error) {
	ss, err := ks.ethPrvKey.Decrypt(ess, nil, nil)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(ss), nil
}
