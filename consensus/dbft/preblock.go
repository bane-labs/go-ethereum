package dbft

import (
	"fmt"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/log"
	"github.com/nspcc-dev/dbft"
)

var _ dbft.PreBlock[common.Hash] = (*PreBlock)(nil)

// PreBlock is a wrapper around Eth block that implements [dbft.PreBlock] interface.
// It holds some initial proposed block's data as far as standard/encrypted
// transactions.
type PreBlock struct {
	header       *types.Header
	withdrawals  []*types.Withdrawal
	transactions []*types.Transaction
	// localShares holds serialized local TPKE shares containing two entries:
	// encoded slice of the current round shares followed by encoded slice of
	// the previous round shares.
	localShares []byte

	// envelopesData is the ordered list of decoded content of Envelopes
	// transactions of the proposed PreBlock. In case if enforceECDSASignatures
	// is enabled, an attempt to decode Envelope transactions won't be taken, hence
	// envelopesData will be empty.
	envelopesData []envelopeData
	// enforceECDSASignatures reflects whether dBFT uses backup multisignature
	// block signing scheme.
	enforceECDSASignatures bool
	// dkgRound is the index of DKG round for the current proposal according to
	// KeyManagement system contract.
	dkgRound uint32

	// finalTransactions is the cached final list of transactions formed after TPKE
	// decryption of Envelopes content. This list includes both simple standard and
	// decrypted transactions.
	finalTransactions []*types.Transaction
	// final* fields below represent information got after finalTransactions
	// processing.
	finalState    *state.StateDB
	finalGASUsed  uint64
	finalReceipts []*types.Receipt
}

// Data implements [dbft.PreBlock] interface.
func (p *PreBlock) Data() []byte { return p.localShares }

// SetData implements [dbft.PreBlock] interface.
func (p *PreBlock) SetData(pk dbft.PrivateKey) error {
	var (
		sharesCurr []*tpke.DecryptionShare
		sharesPrev []*tpke.DecryptionShare
	)
	if !p.enforceECDSASignatures {
		var (
			// The most common usage scenario is decryption of transactions from the same
			// DKG epoch, hence, don't allocate the list of encryptedKeysPrev in advance.
			encryptedKeysCurr = make([]*tpke.CipherText, 0, len(p.envelopesData))
			encryptedKeysPrev []*tpke.CipherText
			err               error
		)
		for _, d := range p.envelopesData {
			if d.dkgRound == p.dkgRound {
				encryptedKeysCurr = append(encryptedKeysCurr, d.encryptedKey)
			} else {
				encryptedKeysPrev = append(encryptedKeysPrev, d.encryptedKey)
			}
		}

		// Use "try our best" approach and proceed without decryption if error happens.
		sharesCurr, err = pk.(*Signer).AmevKeystore.DecryptWithShare(encryptedKeysCurr)
		if err != nil {
			log.Error("failed to construct decryption shares for the current DKG round, skipping",
				"round", p.dkgRound,
				"error", err)
		}
		if p.dkgRound >= crossEpochDecryptionStartRound {
			sharesPrev, err = pk.(*Signer).AmevKeystore.DecryptWithReshare(encryptedKeysPrev)
			if err != nil {
				log.Error("failed to construct decryption shares for the previous DKG round, skipping",
					"round", p.dkgRound-1,
					"error", err)
			}
		}
	}

	p.localShares = encodeShares(sharesCurr, sharesPrev)
	return nil
}

// Verify implements [dbft.PreBlock] interface.
func (p *PreBlock) Verify(pub dbft.PublicKey, data []byte) error {
	sharesCurr, sharesPrev, err := decodeShares(data)
	if err != nil {
		return fmt.Errorf("failed to decode decryption shares: %w", err)
	}
	n := len(sharesCurr) + len(sharesPrev)
	if n > len(p.envelopesData) {
		return fmt.Errorf("invalid decryption shares count: expected not more than %d, got %d/%d", len(p.envelopesData), len(sharesCurr), len(sharesPrev))
	}
	if n != len(p.envelopesData) {
		log.Error("some decryption shares are missing",
			"validator", pub.(*PublicKey).Account,
			"expected", len(p.envelopesData),
			"got for current round", len(sharesCurr),
			"got for previous round", len(sharesPrev))
	}
	return nil
}

// Transactions implements [dbft.PreBlock] interface.
func (b *PreBlock) Transactions() []dbft.Transaction[common.Hash] {
	dst := make([]dbft.Transaction[common.Hash], len(b.transactions))
	for i, tx := range b.transactions {
		dst[i] = &Transaction{
			Tx: tx,
		}
	}
	return dst
}

// SetTransactions implements [dbft.PreBlock] interface. txx may contain encrypted
// Envelope transactions.
func (b *PreBlock) SetTransactions(txx []dbft.Transaction[common.Hash]) {
	var (
		txs       = make([]*types.Transaction, len(txx))
		envelopes []envelopeData // don't allocate, Envelopes supposed to be rare.
	)
	for i, tx := range txx {
		txs[i] = tx.(*Transaction).Tx
		if !b.enforceECDSASignatures && antimev.IsEnvelope(txs[i]) {
			d, err := decodeEnvelopeData(txs[i].Data())
			if err != nil {
				// Not an Envelope in fact since it contains malformed data. Include
				// it as a simple transaction.
				continue
			}
			if d.dkgRound < min(1, b.dkgRound-1) || b.dkgRound < d.dkgRound {
				// Envelope not from current/previous DKG round, won't be decoded.
				continue
			}
			d.index = i
			envelopes = append(envelopes, d)
		}
	}
	b.transactions = txs
	b.envelopesData = envelopes
}

// ToEthBlock converts [dbft.PreBlock] to [types.Block].
func (b *PreBlock) ToEthBlock() *types.Block {
	// Uncles are always nil in dBFT-like consensus.
	return types.NewBlockWithHeader(b.header).WithBody(types.Body{Transactions: b.transactions, Withdrawals: b.withdrawals})
}
