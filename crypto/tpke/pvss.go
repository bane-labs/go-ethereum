package tpke

import (
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto/bls12381"
	"github.com/ethereum/go-ethereum/rlp"
)

type PVSS struct {
	commitment *Commitment         // The commitment of local secret polynomial
	r1         *bls12381.PointG1   // The commitment of of random r
	r2         *bls12381.PointG2   // The verifiable commitment of of r1
	bigf       []*bls12381.PointG1 // The commitment of secret sharing
}

var (
	_ rlp.Encoder = &PVSS{}
	_ rlp.Decoder = &PVSS{}
)

type pvssAux struct {
	Commitment *Commitment         // The commitment of local secret polynomial
	R1         *bls12381.PointG1   // The commitment of of random r
	R2         *bls12381.PointG2   // The verifiable commitment of of r1
	Bigf       []*bls12381.PointG1 // The commitment of secret sharing
}

func (p *PVSS) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &pvssAux{
		Commitment: p.commitment,
		R1:         p.r1,
		R2:         p.r2,
		Bigf:       p.bigf,
	})
}

// DecodeRLP decodes recoveryMessage from RLP.
func (p *PVSS) DecodeRLP(s *rlp.Stream) error {
	aux := &pvssAux{}
	if err := s.Decode(aux); err != nil {
		return err
	}
	p.commitment = aux.Commitment
	p.r1 = aux.R1
	p.r2 = aux.R2
	p.bigf = aux.Bigf
	return nil
}

// GenerateSecretShares takes a random r to generate PVSS
func GenerateSecretShares(r *big.Int, size int, secret *Secret) (*PVSS, []*big.Int) {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	r1 := g1.New()
	r2 := g2.New()
	g1.MulScalar(r1, g1.One(), r)
	g2.MulScalar(r2, g2.One(), r)
	g2.Neg(r2, r2)
	f := make([]*big.Int, size)
	bigf := make([]*bls12381.PointG1, size)
	for i := 0; i < size; i++ {
		// Start from 1
		fr := big.NewInt(int64(i + 1))
		// Compute secret share f(i)
		f[i] = secret.poly.evaluate(fr)
		// Compute public share F(i)=f(i)*G1
		bigf[i] = secret.poly.commitment().evaluate(fr)
	}
	return &PVSS{
		commitment: secret.Commitment(),
		r1:         r1,
		r2:         r2,
		bigf:       bigf,
	}, f
}

func (pvss *PVSS) GetCommitment() *Commitment { return pvss.commitment }

func (pvss *PVSS) ToBytes() []byte {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	arr := make([]byte, 0)
	arr = append(arr, pvss.commitment.ToBytes()...)
	arr = append(arr, g1.EncodePoint(pvss.r1)...)
	arr = append(arr, g2.EncodePoint(pvss.r2)...)
	for i := 0; i < len(pvss.bigf); i++ {
		arr = append(arr, g1.EncodePoint(pvss.bigf[i])...)
	}
	return arr
}

func (pvss *PVSS) FromBytes(b []byte, n int, t int) (*PVSS, error) {
	if len(b) != (t+n+1)*128+256 {
		return nil, ErrTPKEDecoding
	}
	comm, err := new(Commitment).FromBytes(b[:t*128], t)
	if err != nil {
		return nil, err
	}
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	r1, err := g1.DecodePoint(b[t*128 : (t+1)*128])
	if err != nil {
		return nil, err
	}
	r2, err := g2.DecodePoint(b[(t+1)*128 : (t+1)*128+256])
	if err != nil {
		return nil, err
	}
	bigf := make([]*bls12381.PointG1, n)
	for i := 0; i < n; i++ {
		pg1, err := g1.DecodePoint(b[(t+1)*128+256+i*128 : (t+1)*128+256+(i+1)*128])
		if err != nil {
			return nil, err
		}
		bigf[i] = pg1
	}
	pvss.commitment = comm
	pvss.r1 = r1
	pvss.r2 = r2
	pvss.bigf = bigf
	return pvss, nil
}

// VerifyCommitment verifies a PVSS based on its commitment
func (pvss *PVSS) VerifyCommitment() bool {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	// Verify e(R1,G2)==e(G1,-R2)
	pairing := bls12381.NewPairingEngine()
	pairing.AddPair(pvss.r1, g2.One())
	pairing.AddPair(g1.One(), pvss.r2)
	if !pairing.Check() {
		return false
	}
	for i := 0; i < len(pvss.bigf); i++ {
		fr := big.NewInt(int64(i + 1))
		// Verify F(i)==sum(A_{t-1}*i^(t-1))
		if !g1.Equal(pvss.bigf[i], pvss.commitment.evaluate(fr)) {
			return false
		}

	}
	return true
}

// VerifySecret verifies a PVSS based on shared secret
func (pvss *PVSS) VerifySecret(index int, fi *big.Int) bool {
	g1 := bls12381.NewG1()
	g2 := bls12381.NewG2()
	pairing := bls12381.NewPairingEngine()
	// e(r1*fi,g2)=e(bigfi,-r2)
	pairing.AddPair(g1.MulScalar(g1.New(), pvss.r1, fi), g2.One())
	pairing.AddPair(pvss.bigf[index], pvss.r2)
	return pairing.Check()
}

// VerifyRenovate verifies if a PVSS renovate correctly for resharing
func (pvss *PVSS) VerifyRenovate(op *PVSS) bool {
	// Verify the new pvss bigf has the same A0
	if len(pvss.commitment.coeff) != len(op.commitment.coeff) {
		return false
	}
	g1 := bls12381.NewG1()
	return g1.Equal(pvss.commitment.coeff[0], op.commitment.coeff[0])
}
