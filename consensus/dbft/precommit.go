package dbft

import (
	"encoding/binary"
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
	cached     bool
	sharesCurr []*tpke.DecryptionShare
	sharesPrev []*tpke.DecryptionShare
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
	var (
		aux preCommitAux
		err error
	)
	if err = s.Decode(&aux); err != nil {
		return err
	}

	m.dataExt = aux.DataExt
	m.sharesCurr, m.sharesPrev, err = decodeShares(m.dataExt)
	if err != nil {
		return fmt.Errorf("failed to decode shares: %w", err)
	}
	m.cached = true

	return nil
}

// Shares returns the slice of tpke.DecryptionShare that preCommit carries.
func (m *preCommit) Shares() ([]*tpke.DecryptionShare, []*tpke.DecryptionShare) {
	if !m.cached {
		var err error
		m.sharesCurr, m.sharesPrev, err = decodeShares(m.dataExt)
		if err != nil {
			panic(fmt.Errorf("bug: invalid locally constructed shares: %w", err))
		}
		m.cached = true
	}
	return m.sharesCurr, m.sharesPrev
}

// encodeShares encodes the slice of tpke.DecryptionShare in the same way as it's
// required by preCommit serialization rules.
func encodeShares(sharesCurr []*tpke.DecryptionShare, sharesPrev []*tpke.DecryptionShare) []byte {
	var res = make([]byte, 4*2, 4*2+tpke.DecryptionShareSize*(len(sharesCurr)+len(sharesPrev)))
	binary.LittleEndian.PutUint32(res, uint32(len(sharesCurr)))
	binary.LittleEndian.PutUint32(res[4:], uint32(len(sharesPrev)))
	for i := range sharesCurr {
		res = append(res, sharesCurr[i].ToBytes()...)
	}
	for i := range sharesPrev {
		res = append(res, sharesPrev[i].ToBytes()...)
	}
	return res
}

// decodeShares decodes the list of tpke.DecryptionShare from the provided
// data following preCommit serialization rules.
func decodeShares(data []byte) ([]*tpke.DecryptionShare, []*tpke.DecryptionShare, error) {
	offset := 8
	if len(data) < offset {
		return nil, nil, fmt.Errorf("shares slice is too short: expected at least %d bytes, got %d", offset, len(data))
	}

	nCurr := binary.LittleEndian.Uint32(data[:4])
	nPrev := binary.LittleEndian.Uint32(data[4:offset])
	n := nCurr + nPrev
	if n > maxDecryptionSharesPerBlock {
		return nil, nil, fmt.Errorf("too many shares: got %d/%d, allowed %d at max", nCurr, nPrev, maxDecryptionSharesPerBlock)
	}
	expectedLen := offset + int(n*tpke.DecryptionShareSize)
	if len(data) != expectedLen {
		return nil, nil, fmt.Errorf("shares slise has invalid length: expected %d, got %d", expectedLen, len(data))
	}

	var (
		curr = make([]*tpke.DecryptionShare, nCurr)
		prev = make([]*tpke.DecryptionShare, nPrev)
	)
	for i := range curr {
		curr[i] = &tpke.DecryptionShare{}
		_, err := curr[i].FromBytes(data[offset : offset+tpke.DecryptionShareSize])
		if err != nil {
			fmt.Errorf("decoding current round share %d: %w", i, err)
		}
		offset += tpke.DecryptionShareSize
	}
	for i := range prev {
		prev[i] = &tpke.DecryptionShare{}
		_, err := prev[i].FromBytes(data[offset : offset+tpke.DecryptionShareSize])
		if err != nil {
			fmt.Errorf("decoding previous round share %d: %w", i, err)
		}
		offset += tpke.DecryptionShareSize
	}

	return curr, prev, nil
}
