package dbft

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/nspcc-dev/dbft"
)

// preCommit represents dBFT PreCommit message.
type preCommit struct {
	// DataExt is a serialized set of shares ordered by the same way as encrypted
	// transactions in block
	DataExt []byte
}

var _ dbft.PreCommit = (*preCommit)(nil)

// Data implements the payload.PreCommit interface.
func (p preCommit) Data() []byte { return p.DataExt }

// UnmarshalShares unmarshal decryption share produced by validator that have sent
// this preCommit messages. The resulting shares order matches the order of Envelopes
// in the provided set of transactions.
func decodeEnvelopesData(txs []*types.Transaction) []envelopeData {
	var res []envelopeData
	for i, tx := range txs {
		if isEnvelope(tx) {
			d, err := decodeEnvelopeData(tx.Data())
			if err != nil {
				// TODO: Envelope content unmarshalling may be checked during PrepareRequest level
				// when all proposed in-block transactions are received. I'd suggest to do this
				// and cache the result in the dBFT context for the current view. Another approach
				// is to accept all Envelopes unchecked and if unmarshalling at the PreCommit stage
				// fails, then just skip the content decryption and include Envelope into block "as is".
				continue
			}
			d.index = i
			res = append(res, d)
		}
	}
	return res
}

type envelopeData struct {
	index        int              // index of the corresponding Envelope transaction in the block.
	prefix       []byte           // TODO: this prefix is 4 constant bytes, thus we may remove this field.
	encryptedKey *tpke.CipherText // TODO: ensure that Envelope verification rules include length check for encryptedKey
	encryptedMsg []byte
}

func decodeEnvelopeData([]byte) (envelopeData, error) {
	// TODO: implement RLP encoding/decoding for envelopeData
	panic("TODO")
}

// TODO: consider reusing this structure instead of
type share struct {
	txIndex int // index of transaction in block.
	share   *tpke.DecryptionShare
}
