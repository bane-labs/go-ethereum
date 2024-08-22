package dbft

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft"
)

// maxDecryptionShares is the maximum number of shares that preCommit message allowed
// to carry. This number is actually restricted by the maximum number of Envelope
// transactions per block, but this value depends on floating fees, thus
// maxDecryptionSharesPerBlock is intentionally higher than max Envelopes per block
// and may be accepted only at preCommit decoding level.
const maxDecryptionSharesPerBlock = 1000

// preCommit represents dBFT PreCommit message.
type preCommit struct {
	// dataExt is a serialized set of shares ordered by the same way as Envelope
	// transactions in the proposed block. This field is always not empty because
	// of the way how preCommits are created/received by node.
	dataExt []byte

	// cached indicates whether shares content is decoded from dataExt.
	cached bool
	shares []*tpke.DecryptionShare
}

// preCommitAux is an auxiliary structure used for preCommit RLP encoding.
type preCommitAux struct {
	DataExt []byte
}

var _ dbft.PreCommit = (*preCommit)(nil)

// Data implements the payload.PreCommit interface.
func (p *preCommit) Data() []byte {
	return p.dataExt
}

// EncodeRLP serializes preCommit as RLP.
func (m *preCommit) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &preCommitAux{
		DataExt: m.dataExt,
	})
}

// DecodeRLP decodes preCommit from RLP.
func (m *preCommit) DecodeRLP(s *rlp.Stream) error {
	var aux preCommitAux
	if err := s.Decode(&aux); err != nil {
		return err
	}

	m.dataExt = aux.DataExt
	err := m.decodeShares()
	if err != nil {
		return fmt.Errorf("decode shares: %w", err)
	}

	return nil
}

// Shares returns the slice of tpke.DecryptionShare that preCommit carries.
func (m *preCommit) Shares() []*tpke.DecryptionShare {
	if !m.cached {
		err := m.decodeShares()
		if err != nil {
			panic(fmt.Errorf("bug: invalid locally constructed shares: %w", err))
		}
	}
	return m.shares
}

// encodeShares encodes the slice of tpke.DecryptionShare in the same way as it's
// required by preCommit serialization rules.
func encodeShares(shares []*tpke.DecryptionShare) []byte {
	var res = make([]byte, 4, tpke.DecryptionShareSize*len(shares)+4)
	binary.LittleEndian.PutUint32(res, uint32(len(shares)))
	for i := range shares {
		res = append(res, shares[i].ToBytes()...)
	}
	return res
}

// decodeShares decodes the list of tpke.DecryptionShare from the underlying
// preCommit data.
func (m *preCommit) decodeShares() error {
	if len(m.dataExt) < 4 {
		return errors.New("shares slice is too short")
	}
	n := binary.LittleEndian.Uint32(m.dataExt[:4])
	if n > maxDecryptionSharesPerBlock {
		return fmt.Errorf("too many shares: got %d, allowed %d", n, maxDecryptionSharesPerBlock)
	}
	m.shares = make([]*tpke.DecryptionShare, n)
	for i := range m.shares {
		m.shares[i] = &tpke.DecryptionShare{}
		_, err := m.shares[i].FromBytes(m.dataExt[4+i*tpke.DecryptionShareSize : 4+(i+1)*tpke.DecryptionShareSize])
		if err != nil {
			fmt.Errorf("share %d: %w", i, err)
		}
	}
	m.cached = true
	return nil
}
