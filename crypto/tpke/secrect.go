package tpke

import (
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/rlp"
)

type Secret struct {
	poly *Poly // a local secret polynomial for secret sharing
}

var (
	_ rlp.Encoder = &Secret{}
	_ rlp.Decoder = &Secret{}
)

type secretAux struct {
	Poly *Poly
}

func (sec *Secret) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &secretAux{sec.poly})
}

// DecodeRLP decodes recoveryMessage from RLP.
func (sec *Secret) DecodeRLP(s *rlp.Stream) error {
	aux := &secretAux{}
	if err := s.Decode(aux); err != nil {
		return err
	}
	sec.poly = aux.Poly
	return nil
}

// RandomSecret returns a random polynomial with zero δ
func RandomSecret(threshold int) *Secret {
	return &Secret{
		poly: randomPoly(threshold),
	}
}

// RecoverSecret tries to recover a polynomial with (x,fx) array
func RecoverSecret(is []int, fis []*big.Int) *Secret {
	return &Secret{
		poly: &Poly{
			coeff: polyRecover(is, fis),
		},
	}
}

// Renovate returns a new secret random a1..an-1 expect a0
func (s *Secret) Renovate() *Secret {
	poly := randomPoly(len(s.poly.coeff))
	poly.coeff[0].Set(s.poly.coeff[0])
	return &Secret{
		poly: poly,
	}
}

func (s *Secret) Commitment() *Commitment {
	return s.poly.commitment()
}

func (s *Secret) Evaluate(x *big.Int) *big.Int {
	return s.poly.evaluate(x)
}

func (s *Secret) Equals(other *Secret) bool {
	if len(s.poly.coeff) != len(other.poly.coeff) {
		return false
	}

	for i := range s.poly.coeff {
		if s.poly.coeff[i].Cmp(other.poly.coeff[i]) != 0 {
			return false
		}
	}

	return true
}
