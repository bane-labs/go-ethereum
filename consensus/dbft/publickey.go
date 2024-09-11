package dbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/nspcc-dev/dbft"
)

var _ = dbft.PublicKey(&PublicKey{})

// PublicKey is a wrapper that implements dbftCrypto.PublicKey interface and is
// sufficient for dBFT operations.
type PublicKey struct {
	Account common.Address
	// Eth block signatures slightly different from Neo's once, and Key is
	// currently not needed for dBFT operations. It may be required for
	// verification later.
	Key any
}
