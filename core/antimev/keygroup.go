package antimev

import (
	"errors"
	"io"
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/rlp"
)

var (
	ErrMessageEncryption    = errors.New("message encryption failed")
	ErrDKGPVSS              = errors.New("invalid dkg pvss")
	ErrDKGSecret            = errors.New("invalid dkg secret")
	ErrNoSecretToReshare    = errors.New("no secret to reshare")
	ErrInvalidReshare       = errors.New("invalid reshare")
	ErrInvalidRecover       = errors.New("invalid recover")
	ErrSecretShareNotEnough = errors.New("secret share not enough")
)

// thresholdKeyGroup records the process and result of a round of dkg.
type thresholdKeyGroup struct {
	holders []*thresholdKeyHolder // Members of this dkg group
	pvsses  []*tpke.PVSS          // Public verifiable sharing commitments

	selfAddr        common.Address // Self address
	localSecret     *tpke.Secret   // The local random secret
	receivedSecrets []*big.Int     // Received secret sharings

	globalPubKey *tpke.PublicKey  // The aggregated global public key
	localPrvKey  *tpke.PrivateKey // The aggregated local secret key
}

// thresholdKeyHolder is a message receiver in dkg.
type thresholdKeyHolder struct {
	address   common.Address   // The account address
	ethPubKey *ecies.PublicKey // The account public key
}

var (
	_ rlp.Encoder = &thresholdKeyGroup{}
	_ rlp.Decoder = &thresholdKeyGroup{}
)

// thresholdKeyGroupAux is an auxiliary structure for thresholdKeyGroup RLP marshalling.
type thresholdKeyGroupAux struct {
	Holders []*thresholdKeyHolder // Members of this dkg group
	Pvsses  []*tpke.PVSS          // Public verifiable sharing commitments

	SelfAddr        common.Address // Self address
	LocalSecret     *tpke.Secret   // The local random secret
	ReceivedSecrets [][]byte       // Received secret sharings

	GlobalPubKey *tpke.PublicKey  // The aggregated global public key
	LocalPrvKey  *tpke.PrivateKey // The aggregated local secret key
}

// EncodeRLP implements [rlp.Encoder].
func (tkg *thresholdKeyGroup) EncodeRLP(w io.Writer) error {
	secrets := make([][]byte, len(tkg.receivedSecrets))
	for i, s := range tkg.receivedSecrets {
		secrets[i] = s.Bytes()
	}
	return rlp.Encode(w, thresholdKeyGroupAux{
		Holders:         tkg.holders,
		Pvsses:          tkg.pvsses,
		SelfAddr:        tkg.selfAddr,
		LocalSecret:     tkg.localSecret,
		ReceivedSecrets: secrets,
		GlobalPubKey:    tkg.globalPubKey,
		LocalPrvKey:     tkg.localPrvKey,
	})
}

// DecodeRLP implements [rlp.Decoder].
func (tkg *thresholdKeyGroup) DecodeRLP(s *rlp.Stream) error {
	aux := &thresholdKeyGroupAux{}
	if err := s.Decode(aux); err != nil {
		return err
	}
	var secrets []*big.Int
	for _, s := range aux.ReceivedSecrets {
		secret := new(big.Int).SetBytes(s)
		secrets = append(secrets, secret)
	}
	tkg.holders = aux.Holders
	tkg.pvsses = aux.Pvsses
	tkg.selfAddr = aux.SelfAddr
	tkg.localSecret = aux.LocalSecret
	tkg.receivedSecrets = secrets
	tkg.globalPubKey = aux.GlobalPubKey
	tkg.localPrvKey = aux.LocalPrvKey
	return nil
}

var (
	_ rlp.Encoder = &thresholdKeyHolder{}
	_ rlp.Decoder = &thresholdKeyHolder{}
)

// thresholdKeyHolderAux is an auxiliary structure for RLP thresholdKeyHolder marshalling.
type thresholdKeyHolderAux struct {
	Address   common.Address   // The account address
	EthPubKey *ecies.PublicKey // The account public key
}

// EncodeRLP implements [rlp.Encoder].
func (h *thresholdKeyHolder) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &thresholdKeyHolderAux{
		Address:   h.address,
		EthPubKey: h.ethPubKey,
	})
}

// DecodeRLP implements [rlp.Decoder].
func (h *thresholdKeyHolder) DecodeRLP(s *rlp.Stream) error {
	aux := &thresholdKeyHolderAux{}
	if err := s.Decode(aux); err != nil {
		return err
	}
	h.address = aux.Address
	h.ethPubKey = aux.EthPubKey
	return nil
}

// newThresholdKeyGroup returns a new key group with the input address list.
func newThresholdKeyGroup(selfAddr common.Address, validators []common.Address, pubkeys []*ecies.PublicKey) *thresholdKeyGroup {
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
		selfAddr:        selfAddr,
		pvsses:          make([]*tpke.PVSS, size),
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
	// Refer pvss and global key for verification
	return &thresholdKeyGroup{
		holders:         holders,
		selfAddr:        tkg.selfAddr,
		pvsses:          tkg.pvsses,
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
	// Copy pvss and refer global key for verification
	pvsses := make([]*tpke.PVSS, size)
	copy(pvsses, tkg.pvsses)
	return &thresholdKeyGroup{
		holders:         holders,
		selfAddr:        tkg.selfAddr,
		pvsses:          pvsses,
		receivedSecrets: make([]*big.Int, size),
		globalPubKey:    tkg.globalPubKey,
	}
}

// dkgPrepare generates local secrets and returns sharing messages.
// selfIndex is the dkg member index which starts from 1.
// ethPrvKey is the sender's eth secret key for signing.
func (tkg *thresholdKeyGroup) dkgPrepare(threshold int) ([][]byte, *tpke.PVSS, error) {
	// Generate nothing if not a member
	selfIndex := tkg.holderIndex(tkg.selfAddr)
	if selfIndex < 1 {
		return nil, nil, nil
	}
	// Generate local secret
	tkg.localSecret = tpke.RandomSecret(threshold)
	// Generate and encrypt messages to share the secret
	return generateShareMessages(tkg.localSecret, selfIndex, tkg.holders)
}

// dkgAggregate aggregates received secrets and pvss to get global public key and
// a local piece of secret key for message decryption and signing.
func (tkg *thresholdKeyGroup) dkgAggregate(scaler int) error {
	// Compute public key S=sum(A0)
	scs := make([]*tpke.Commitment, len(tkg.holders))
	for i, pvss := range tkg.pvsses {
		if pvss == nil {
			return ErrSecretShareNotEnough
		}
		scs[i] = pvss.GetCommitment()
	}
	globalPubKey := tpke.NewGlobalPublicKey(scs, scaler)
	// Verify the key if this is resharing
	if tkg.globalPubKey == nil {
		tkg.globalPubKey = globalPubKey
	} else {
		if !tkg.globalPubKey.Equal(globalPubKey) {
			return ErrInvalidReshare
		}
	}
	// Compute local secret key
	if tkg.holderIndex(tkg.selfAddr) > 0 {
		tkg.localPrvKey = tpke.NewPrivateKey(tkg.receivedSecrets)
	}
	return nil
}

// dkgRecover returns received shared secrets with different message encryption.
func (tkg *thresholdKeyGroup) dkgRecover(secretIndexs []int, receiverEthPubKeys []*ecies.PublicKey) ([][]byte, error) {
	// Share nothing if not a member
	selfIndex := tkg.holderIndex(tkg.selfAddr)
	if selfIndex < 1 {
		return nil, nil
	}
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

// dkgReshare does almost the same as dkgPrepare, but local secret is reused to
// produce the same global public key.
func (tkg *thresholdKeyGroup) dkgReshare(target *thresholdKeyGroup) ([][]byte, *tpke.PVSS, error) {
	// Share nothing if not a member
	selfIndex := tkg.holderIndex(tkg.selfAddr)
	if selfIndex < 1 {
		return nil, nil, nil
	}
	// Check if has a local secret
	if tkg.localSecret == nil {
		return nil, nil, ErrNoSecretToReshare
	}
	// Generate and encrypt messages to share the secret
	return generateShareMessages(tkg.localSecret.Renovate(), selfIndex, target.holders)
}

func (tkg *thresholdKeyGroup) dkgReshareRecovered(threshold int, target *thresholdKeyGroup) ([][]byte, *tpke.PVSS, error) {
	// Do nothing if not a receiver
	if tkg.holderIndex(tkg.selfAddr) < 1 {
		return nil, nil, nil
	}
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
	return tkg.dkgReshare(target)
}

// generateShareMessages generates secret sharing messages.
// Secret shares can be decrypted by specific receivers, but pvss is public.
func generateShareMessages(secret *tpke.Secret, selfIndexIndex int, receivers []*thresholdKeyHolder) ([][]byte, *tpke.PVSS, error) {
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
func (tkg *thresholdKeyGroup) receiveShareMessage(fromIndex int, ss *big.Int, pvss *tpke.PVSS) error {
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify the commitment
	if !pvss.VerifyCommitment() {
		return ErrDKGPVSS
	}
	// If is a member of receiver
	if ss != nil {
		// Verify with pvss
		if !pvss.VerifySecret(tkg.holderIndex(tkg.selfAddr)-1, ss) {
			return ErrDKGSecret
		}
		// Store ss for local secret key generation
		tkg.receivedSecrets[arrIndex] = ss
	}
	// Store pvss for global public key generation
	tkg.pvsses[arrIndex] = pvss
	return nil
}

// receiveReshareMessage verifies received resharing messages. It verifies shared
// secret if is a member, otherwise only the pvss. Received data will be stored
// in thresholdKeyGroup for further aggregation.
func (tkg *thresholdKeyGroup) receiveReshareMessage(fromIndex int, rs *big.Int, pvss *tpke.PVSS) error {
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify the commitment
	if !pvss.VerifyCommitment() {
		return ErrDKGPVSS
	}
	// Check delta if resharing
	if tkg.pvsses[arrIndex] != nil && !pvss.VerifyRenovate(tkg.pvsses[arrIndex]) {
		return ErrDKGPVSS
	}
	// If is a member of receiver
	if rs != nil {
		// Verify with pvss
		if !pvss.VerifySecret(tkg.holderIndex(tkg.selfAddr)-1, rs) {
			return ErrDKGSecret
		}
		// Store ss for local secret key generation
		tkg.receivedSecrets[arrIndex] = rs
	}
	// Store pvss for global public key generation
	tkg.pvsses[arrIndex] = pvss
	return nil
}

// receiveRecoverMessage verifies received recovering messages. It verifies shared
// secret if is a member, with the existing pvss. Received data will be stored
// in thresholdKeyGroup for further aggregation.
func (tkg *thresholdKeyGroup) receiveRecoverMessage(fromIndex int, rs *big.Int) error {
	// Do nothing if not a member
	selfIndex := tkg.holderIndex(tkg.selfAddr)
	if selfIndex < 1 {
		return nil
	}
	// Transform dkg index to array index
	arrIndex := fromIndex - 1
	// Verify with pvss
	if !tkg.pvsses[selfIndex-1].VerifySecret(arrIndex, rs) {
		return ErrDKGSecret
	}
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
