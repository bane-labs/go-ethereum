package dbft

import (
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/consensus/dbft/dbftutil"
	"github.com/ethereum/go-ethereum/crypto/tpke"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/nspcc-dev/dbft"
)

// commit represents dBFT Commit message.
type commit struct {
	// version holds a version of block Extra data at the commit's height. This field
	// is filled manually either in [dbft.WithNewCommit] callback or based on the
	// consensus [message] height during [message] RLP decoding.
	version dbftutil.ExtraVersion
	// signature holds multisignature bytes in case of [dbftutil.ExtraV0] commit
	// version and threshold signature share bytes in case of [dbftutil.ExtraV1]
	// commit version.
	signature []byte
	// shareCache holds [tpke.SignatureShare] deserialized from the signature in
	// case of [dbftutil.ExtraVersionV1]. Use [commit.share] to properly access this
	// field.
	shareCache *tpke.SignatureShare
}

// commitAux is an auxiliary structure for commit RLP marshalling.
type commitAux struct {
	Signature []byte
}

var _ dbft.Commit = (*commit)(nil)

// Signature implements the payload.Commit interface.
func (c commit) Signature() []byte { return c.signature[:] }

// EncodeRLP serializes commit to RLP.
func (c commit) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &commitAux{
		Signature: c.signature,
	})
}

// DecodeRLP decodes commit from RLP.
func (c *commit) DecodeRLP(s *rlp.Stream) error {
	var aux commitAux
	if err := s.Decode(&aux); err != nil {
		return err
	}

	c.signature = aux.Signature

	return nil
}

// share returns [tpke.SignatureShare] unpacked from commit's signature in case of
// [dbftutil.ExtraV1] commit version. No error is returned for other commit versions.
func (c *commit) share() (*tpke.SignatureShare, error) {
	if c.version != dbftutil.ExtraV1 {
		return nil, nil
	}
	if c.shareCache == nil {
		c.shareCache = new(tpke.SignatureShare)
		_, err := c.shareCache.FromBytes(c.signature)
		if err != nil {
			return nil, fmt.Errorf("invalid signature share: %w", err)
		}

	}
	return c.shareCache, nil
}
