package antimev

import (
	"encoding/hex"
	"errors"
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	ErrMessageEncryption    = errors.New("message encryption failed")
	ErrDKGIndex             = errors.New("invalid dkg index")
	ErrDKGSecret            = errors.New("invalid dkg secret")
	ErrNoSecretToReshare    = errors.New("no secret to reshare")
	ErrInvalidReshare       = errors.New("invalid reshare")
	ErrInvalidRecover       = errors.New("invalid recover")
	ErrSecretShareNotEnough = errors.New("secret share not enough")
)

// thresholdKeyGroup records the process and result of a round of dkg.
// PVSS is verified in DKG contract and used as input parameters here.
type thresholdKeyGroup struct {
	holders []*thresholdKeyHolder // Members of this dkg group

	localSecret     *tpke.Secret // The local random secret
	receivedSecrets []*big.Int   // Received secret sharings

	globalPubKey *tpke.PublicKey  // The aggregated global public key
	localPrvKey  *tpke.PrivateKey // The aggregated local secret key
}

// thresholdKeyHolder is a message receiver in dkg.
type thresholdKeyHolder struct {
	address   common.Address   // The account address
	ethPubKey *ecies.PublicKey // The account public key
}

// newThresholdKeyGroup returns a new key group with the input address list.
func newThresholdKeyGroup(validators []common.Address, pubkeys []*ecies.PublicKey) *thresholdKeyGroup {
	size := len(validators)
	holders := make([]*thresholdKeyHolder, size)
	for i, validator := range validators {
		holders[i] = &thresholdKeyHolder{
			address:   validator,
			ethPubKey: pubkeys[i],
		}
	}
	return &thresholdKeyGroup{
		holders:         holders,
		receivedSecrets: make([]*big.Int, size),
	}
}

// newTemplateForRecover copies and returns a shared key group for recovering.
func (tkg *thresholdKeyGroup) newTemplateForRecover(indexes []int, validators []common.Address, pubkeys []*ecies.PublicKey) *thresholdKeyGroup {
	size := len(tkg.holders)
	holders := make([]*thresholdKeyHolder, size)
	for i, index := range indexes {
		holders[index-1] = &thresholdKeyHolder{
			address:   validators[i],
			ethPubKey: pubkeys[i],
		}
	}
	// Refer received secrets for recovery
	return &thresholdKeyGroup{
		holders:         holders,
		receivedSecrets: make([]*big.Int, size),
		globalPubKey:    tkg.globalPubKey,
	}
}

// newTemplateForReshare copies and returns a shared key group for resharing.
func (tkg *thresholdKeyGroup) newTemplateForReshare(validators []common.Address, pubkeys []*ecies.PublicKey) *thresholdKeyGroup {
	size := len(validators)
	holders := make([]*thresholdKeyHolder, size)
	// Use the new holder list
	for i, validator := range validators {
		holders[i] = &thresholdKeyHolder{
			address:   validator,
			ethPubKey: pubkeys[i],
		}
	}
	// Refer received secrets for reshare
	return &thresholdKeyGroup{
		holders:         holders,
		receivedSecrets: make([]*big.Int, size),
		globalPubKey:    tkg.globalPubKey,
	}
}

// prepare generates local secrets and returns sharing messages.
func (tkg *thresholdKeyGroup) prepare(threshold int) ([][]byte, *tpke.PVSS, error) {
	// Generate local secret
	tkg.localSecret = tpke.RandomSecret(threshold)
	// Generate and encrypt messages to share the secret
	return generateShareMessages(tkg.localSecret, tkg.holders)
}

// aggregate aggregates received secrets and commitments to get global
// public key and a local piece of secret key for message decryption and signing.
func (tkg *thresholdKeyGroup) aggregate(scaler int, aggregatedCmt []byte, selfIndex int) error {
	if selfIndex > 0 {
		for _, secret := range tkg.receivedSecrets {
			if secret == nil {
				return ErrSecretShareNotEnough
			}
		}
		tkg.localPrvKey = tpke.NewPrivateKey(tkg.receivedSecrets)
	}
	// Get the pubkey from contract aggregated commitments
	if tkg.globalPubKey == nil {
		globalPubKey, err := tpke.NewGlobalPublicKey(aggregatedCmt, scaler)
		if err != nil {
			return err
		}
		tkg.globalPubKey = globalPubKey
	}
	return nil
}

// recover returns received shared secrets with different message encryption.
func (tkg *thresholdKeyGroup) recover(secretIndexs []int, receiverEthPubKeys []*ecies.PublicKey) ([][]byte, error) {
	// Random source for message encryption
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	// Generate message
	srms := make([][]byte, len(secretIndexs))
	for i, index := range secretIndexs {
		arrIndex := index - 1
		ess, err := ecies.Encrypt(random, receiverEthPubKeys[i], tkg.receivedSecrets[arrIndex].Bytes(), nil, nil)
		if err != nil {
			return nil, ErrMessageEncryption
		}
		srms[i] = ess
	}

	return srms, nil
}

// reshare does almost the same as dkgPrepare, but local secret is reused to
// produce the same global public key.
func (tkg *thresholdKeyGroup) reshare(target *thresholdKeyGroup) ([][]byte, *tpke.PVSS, error) {
	// Check if has a local secret
	if tkg.localSecret == nil {
		return nil, nil, ErrNoSecretToReshare
	}
	// Generate and encrypt messages to share the secret
	return generateShareMessages(tkg.localSecret.Renovate(), target.holders)
}

// reshareRecovered tries to recover a dkg secret, and returns an error if
// the attempt fails when shares are not enough.
func (tkg *thresholdKeyGroup) reshareRecovered(threshold int, target *thresholdKeyGroup) ([][]byte, *tpke.PVSS, error) {
	// Collect all shares
	is := make([]int, 0)
	fis := make([]*big.Int, 0)
	for i, ss := range tkg.receivedSecrets {
		if ss != nil {
			is = append(is, i+1)
			fis = append(fis, ss)
		}
	}
	if len(tkg.receivedSecrets) < threshold {
		return nil, nil, ErrInvalidRecover
	}
	// Recover the secret
	tkg.localSecret = tpke.RecoverSecret(is[:threshold], fis[:threshold])
	return tkg.reshare(target)
}

// generateShareMessages generates secret sharing messages.
// Secret shares can be decrypted by specific receivers, but pvss is public.
func generateShareMessages(secret *tpke.Secret, receivers []*thresholdKeyHolder) ([][]byte, *tpke.PVSS, error) {
	// Random source for message encryption
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	// Random value for pvss generation
	randR := randScalar()
	size := len(receivers)
	pvss, ss := tpke.GenerateSecretShares(randR, size, secret)
	// Generate message
	messages := make([][]byte, size)
	for i, receiver := range receivers {
		ess, err := ecies.Encrypt(random, receiver.ethPubKey, ss[i].Bytes(), nil, nil)
		if err != nil {
			return nil, nil, ErrMessageEncryption
		}
		messages[i] = ess
	}

	return messages, pvss, nil
}

// receiveShareMessage verifies received sharing messages. It verifies shared
// secret if is a member, otherwise only the pvss. Received data will be stored
// in thresholdKeyGroup for further aggregation.
func (tkg *thresholdKeyGroup) receiveShareMessage(fromIndex int, ss *big.Int, pvss *tpke.PVSS, selfIndex int) error {
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify with pvss
	if !pvss.VerifySecret(selfIndex-1, ss) {
		return ErrDKGSecret
	}
	// Store ss for local secret key generation
	tkg.receivedSecrets[arrIndex] = ss
	return nil
}

// receiveReshareMessage verifies received resharing messages. It verifies shared
// secret if is a member, otherwise only the pvss. Received data will be stored
// in thresholdKeyGroup for further aggregation.
func (tkg *thresholdKeyGroup) receiveReshareMessage(fromIndex int, rs *big.Int, pvss *tpke.PVSS, selfIndex int) error {
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify with pvss
	if !pvss.VerifySecret(selfIndex-1, rs) {
		return ErrDKGSecret
	}
	// Store ss for local secret key generation
	tkg.receivedSecrets[arrIndex] = rs
	return nil
}

// receiveRecoverMessage verifies received recovering messages. It verifies shared
// secret if is a member, with the existing pvss. Received data will be stored
// in thresholdKeyGroup for further aggregation.
func (tkg *thresholdKeyGroup) receiveRecoverMessage(fromIndex int, rs *big.Int, pvss *tpke.PVSS, selfIndex int) error {
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify with pvss
	if !pvss.VerifySecret(arrIndex, rs) {
		return ErrDKGSecret
	}
	// Store rs for local secret key recovery
	tkg.receivedSecrets[arrIndex] = rs

	return nil
}

// holderIndex is the dkg member index start from 1, 0 means not a member.
func (tkg *thresholdKeyGroup) holderIndex(addr common.Address) int {
	for i, holder := range tkg.holders {
		if holder == nil {
			continue
		}
		if holder.address.Cmp(addr) == 0 {
			return i + 1
		}
	}
	return 0
}

// holderIndexesAndKeys returns a non-empty list of indexes and related pubkeys.
func (tkg *thresholdKeyGroup) holderIndexesAndKeys() ([]int, []*ecies.PublicKey) {
	idxs := make([]int, 0, len(tkg.holders))
	keys := make([]*ecies.PublicKey, 0, len(tkg.holders))
	for i, holder := range tkg.holders {
		if holder == nil {
			continue
		}
		idxs = append(idxs, i+1)
		keys = append(keys, holder.ethPubKey)
	}
	return idxs, keys
}

// thresholdKeyHolderAux is an auxiliary structure for thresholdKeyHolder JSON marshalling.
type thresholdKeyHolderAux struct {
	Address   common.Address `json:"address"`     // The account address
	EthPubKey string         `json:"eth_pub_key"` // The account public key
}

// toAux transforms thresholdKeyHolder to JSON serializable structure.
func (tkh *thresholdKeyHolder) toAux() *thresholdKeyHolderAux {
	return &thresholdKeyHolderAux{
		Address:   tkh.address,
		EthPubKey: hex.EncodeToString(crypto.FromECDSAPub(tkh.ethPubKey.ExportECDSA())),
	}
}

// fromAux transforms deserialized JSON data to thresholdKeyHolder.
func (tkh *thresholdKeyHolder) fromAux(aux *thresholdKeyHolderAux) error {
	tkh.address = aux.Address
	keyBytes, err := hex.DecodeString(aux.EthPubKey)
	if err != nil {
		return err
	}
	pubKey, err := crypto.UnmarshalPubkey(keyBytes)
	if err != nil {
		return err
	}
	tkh.ethPubKey = ecies.ImportECDSAPublic(pubKey)
	return nil
}

// thresholdKeyGroupAux is an auxiliary structure for thresholdKeyGroup JSON marshalling.
type thresholdKeyGroupAux struct {
	Holders []*thresholdKeyHolderAux `json:"holders"` // Members of this dkg group
	Pvsses  []string                 `json:"pvsses"`  // Public verifiable sharing commitments

	SelfAddr        common.Address `json:"self_addr"`        // Self address
	LocalSecret     []*big.Int     `json:"local_secret"`     // The local random secret
	ReceivedSecrets []*big.Int     `json:"received_secrets"` // Received secret sharings

	GlobalPubKey string `json:"global_pubkey"` // The aggregated global public key
	LocalPrvKey  string `json:"local_prvkey"`  // The aggregated local secret key
}

// toAux transforms thresholdKeyGroup to JSON serializable structure.
// Absent fields remain nil, but their positions are allocated.
func (tkg *thresholdKeyGroup) toAux() *thresholdKeyGroupAux {
	aux := &thresholdKeyGroupAux{
		ReceivedSecrets: tkg.receivedSecrets,
	}
	aux.Holders = make([]*thresholdKeyHolderAux, len(tkg.holders))
	for i, v := range tkg.holders {
		// Possible be nil when recovering
		if v != nil {
			aux.Holders[i] = v.toAux()
		}
	}
	// Possible be nil when dkg is undergoing
	if tkg.localSecret != nil {
		aux.LocalSecret = tkg.localSecret.ToBigIntArray()
	}
	if tkg.globalPubKey != nil {
		aux.GlobalPubKey = hex.EncodeToString(tkg.globalPubKey.Bytes())
	}
	if tkg.localPrvKey != nil {
		aux.LocalPrvKey = hex.EncodeToString(tkg.localPrvKey.Bytes())
	}
	return aux
}

// fromAux transforms deserialized JSON data to thresholdKeyGroup.
func (tkg *thresholdKeyGroup) fromAux(aux *thresholdKeyGroupAux) error {
	// Left as nil if not presented
	tkg.receivedSecrets = aux.ReceivedSecrets
	tkg.holders = make([]*thresholdKeyHolder, len(aux.Holders))
	for i, v := range aux.Holders {
		if v != nil {
			tkg.holders[i] = new(thresholdKeyHolder)
			err := tkg.holders[i].fromAux(v)
			if err != nil {
				return err
			}
		}
	}
	if aux.LocalSecret != nil {
		tkg.localSecret = new(tpke.Secret)
		tkg.localSecret.FromBigIntArray(aux.LocalSecret)
	}
	if len(aux.GlobalPubKey) > 0 {
		pubBytes, err := hex.DecodeString(aux.GlobalPubKey)
		if err != nil {
			return err
		}
		pubkey, err := new(tpke.PublicKey).FromBytes(pubBytes)
		if err != nil {
			return err
		}
		tkg.globalPubKey = pubkey
	}
	if len(aux.LocalPrvKey) > 0 {
		prvBytes, err := hex.DecodeString(aux.LocalPrvKey)
		if err != nil {
			return err
		}
		prvkey, err := new(tpke.PrivateKey).FromBytes(prvBytes)
		if err != nil {
			return err
		}
		tkg.localPrvKey = prvkey
	}
	return nil
}
