package antimev

import (
	"encoding/hex"
	"errors"
	"math/big"

	"github.com/bane-labs/zk-dkg/encryption"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
)

var (
	ErrDKGSecret            = errors.New("invalid dkg secret")
	ErrNoSecretToReshare    = errors.New("no secret to reshare")
	ErrInvalidRecover       = errors.New("invalid recover")
	ErrInvalidMessageKey    = errors.New("invalid message key")
	ErrSecretShareNotEnough = errors.New("secret share not enough")
)

// thresholdKeyGroup records the process and result of a round of dkg.
// PVSS is verified in DKG contract and used as input parameters here.
type thresholdKeyGroup struct {
	localSecret     *tpke.Secret   // The final local random secret
	pendingSecrets  []*tpke.Secret // Pending secrets which may get confirmed by contract
	receivedSecrets []*big.Int     // Received secret sharings

	globalPubKey *tpke.PublicKey  // The aggregated global public key
	localPrvKey  *tpke.PrivateKey // The aggregated local secret key
}

// newThresholdKeyGroup returns a new key group with the input address list.
func newThresholdKeyGroup(size int) *thresholdKeyGroup {
	return &thresholdKeyGroup{
		pendingSecrets:  make([]*tpke.Secret, 0),
		receivedSecrets: make([]*big.Int, size),
	}
}

// newTemplateForReshare copies and returns a shared key group for resharing.
func (tkg *thresholdKeyGroup) newTemplateForReshare(size int) *thresholdKeyGroup {
	// Refer received secrets for reshare
	return &thresholdKeyGroup{
		receivedSecrets: make([]*big.Int, size),
		globalPubKey:    tkg.globalPubKey,
	}
}

// copy creates a deep copy of thresholdKeyGroup.
func (tkg *thresholdKeyGroup) copy() *thresholdKeyGroup {
	res := new(thresholdKeyGroup)
	if tkg.localSecret != nil {
		res.localSecret = tkg.localSecret.Copy()
	}
	res.pendingSecrets = make([]*tpke.Secret, len(tkg.pendingSecrets))
	for i := range tkg.pendingSecrets {
		res.pendingSecrets[i] = tkg.pendingSecrets[i].Copy()
	}
	res.receivedSecrets = make([]*big.Int, len(tkg.receivedSecrets))
	for i, secret := range tkg.receivedSecrets {
		if secret != nil {
			res.receivedSecrets[i] = new(big.Int).Set(secret)
		}
	}
	if tkg.globalPubKey != nil {
		res.globalPubKey = tkg.globalPubKey.Copy()
	}
	if tkg.localPrvKey != nil {
		res.localPrvKey = tkg.localPrvKey.Copy()
	}
	return res
}

// prepare generates local secrets and returns sharing messages.
func (tkg *thresholdKeyGroup) prepare(threshold int, messagePubKeys []*ecies.PublicKey) ([][]byte, *tpke.PVSS, error) {
	// Generate local secret
	secret := tpke.RandomSecret(threshold)
	tkg.pendingSecrets = append(tkg.pendingSecrets, secret)
	// Generate and encrypt messages to share the secret
	return generateShareMessages(secret, messagePubKeys)
}

// confirmSecret requires the final contract-received PVSS to confirm the secret.
func (tkg *thresholdKeyGroup) confirmSecret(pvss *tpke.PVSS) error {
	for _, secret := range tkg.pendingSecrets {
		if pvss.IsFrom(secret) {
			tkg.localSecret = secret
			return nil
		}
	}
	return ErrDKGSecret
}

// aggregate aggregates received secrets and commitments to get global
// public key and a local piece of secret key for message decryption and signing.
func (tkg *thresholdKeyGroup) aggregate(scaler int, aggregatedCmt []byte, isReceiver bool) error {
	if isReceiver {
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
	// Generate message
	srms := make([][]byte, len(secretIndexs))
	for i, index := range secretIndexs {
		arrIndex := index - 1
		if tkg.receivedSecrets[arrIndex] == nil {
			return nil, ErrUnrecoverable
		}
		srms[i] = encryptShareMessage(receiverEthPubKeys[i], tkg.receivedSecrets[arrIndex].Bytes())
	}

	return srms, nil
}

// reshare does almost the same as dkgPrepare, but local secret is reused to
// produce the same global public key.
func (tkg *thresholdKeyGroup) reshare(messagePubKeys []*ecies.PublicKey) ([][]byte, *tpke.PVSS, error) {
	// Check if has a local secret
	if tkg.localSecret == nil {
		return nil, nil, ErrNoSecretToReshare
	}
	// Generate and encrypt messages to share the secret
	return generateShareMessages(tkg.localSecret.Renovate(), messagePubKeys)
}

// reshareRecovered tries to recover a dkg secret, and returns an error if
// the attempt fails when shares are not enough.
func (tkg *thresholdKeyGroup) reshareRecovered(threshold int, messagePubKeys []*ecies.PublicKey) ([][]byte, *tpke.PVSS, error) {
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
	return tkg.reshare(messagePubKeys)
}

// generateShareMessages generates secret sharing messages.
// Secret shares can be decrypted by specific receivers, but pvss is public.
func generateShareMessages(secret *tpke.Secret, messagePubkeys []*ecies.PublicKey) ([][]byte, *tpke.PVSS, error) {
	// Random value for pvss generation
	randR := randScalar()
	size := len(messagePubkeys)
	pvss, ss := tpke.GenerateSecretShares(randR, size, secret)
	// Generate message
	messages := make([][]byte, size)
	for i, key := range messagePubkeys {
		if key == nil {
			return nil, nil, ErrInvalidMessageKey
		}
		messages[i] = encryptShareMessage(key, ss[i].Bytes())
	}

	return messages, pvss, nil
}

func encryptShareMessage(pub *ecies.PublicKey, share []byte) []byte {
	nonce, ess, _, bigR := encryption.ECIESEncrypt(pub, share)
	bigRBytes := bigR.RawBytes()
	// len(message)=64+12+len(ess)
	msg := make([]byte, 0)
	msg = append(msg, bigRBytes[:]...)
	msg = append(msg, nonce...)
	msg = append(msg, ess...)
	return msg
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

// thresholdKeyGroupAux is an auxiliary structure for thresholdKeyGroup JSON marshalling.
type thresholdKeyGroupAux struct {
	LocalSecret     []*big.Int   `json:"local_secret"`     // The local random secret
	PendingSecrets  [][]*big.Int `json:"pending_secrets"`  // Sent-but-pending secrets
	ReceivedSecrets []*big.Int   `json:"received_secrets"` // Received secret sharings

	GlobalPubKey string `json:"global_pubkey"` // The aggregated global public key
	LocalPrvKey  string `json:"local_prvkey"`  // The aggregated local secret key
}

// toAux transforms thresholdKeyGroup to JSON serializable structure.
// Absent fields remain nil, but their positions are allocated.
func (tkg *thresholdKeyGroup) toAux() *thresholdKeyGroupAux {
	aux := &thresholdKeyGroupAux{
		ReceivedSecrets: tkg.receivedSecrets,
	}
	// Possible be nil when dkg is undergoing
	if tkg.localSecret != nil {
		aux.LocalSecret = tkg.localSecret.ToBigIntArray()
	}
	aux.PendingSecrets = make([][]*big.Int, 0)
	if len(tkg.pendingSecrets) > 0 {
		for _, secret := range tkg.pendingSecrets {
			aux.PendingSecrets = append(aux.PendingSecrets, secret.ToBigIntArray())
		}
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
	if aux.LocalSecret != nil {
		tkg.localSecret = new(tpke.Secret)
		tkg.localSecret.FromBigIntArray(aux.LocalSecret)
	}
	tkg.pendingSecrets = make([]*tpke.Secret, 0)
	if len(aux.PendingSecrets) > 0 {
		for _, arr := range aux.PendingSecrets {
			secret := new(tpke.Secret)
			secret.FromBigIntArray(arr)
			tkg.pendingSecrets = append(tkg.pendingSecrets, secret)
		}
	}
	if len(aux.GlobalPubKey) > 0 {
		pubBytes, err := hex.DecodeString(aux.GlobalPubKey)
		if err != nil {
			return err
		}
		pubkey, err := tpke.NewPublicKeyFromBytes(pubBytes)
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
