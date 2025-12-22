// Package beacon implements minimized Ethereum beacon client.
package impl

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/triedb"
)

type mockBackend struct {
	bc *core.BlockChain
}

func NewMockBackend(bc *core.BlockChain) *mockBackend {
	return &mockBackend{
		bc: bc,
	}
}

func (m *mockBackend) BlockChain() *core.BlockChain {
	return m.bc
}

func TestBeacon(t *testing.T) {
	t.Parallel()
	beacon, mux, cleanup := createBeacon(t)
	defer cleanup(false)

	beacon.Start()
	waitForMiningState(t, beacon, true)
	// Start the downloader
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, false)
	// Stop the downloader and wait for the update loop to run
	mux.Post(downloader.DoneEvent{})
	waitForMiningState(t, beacon, true)

	// Subsequent downloader events after a successful DoneEvent should not cause the
	// beacon to start or stop. This prevents a security vulnerability
	// that would allow entities to present fake high blocks that would
	// stop mining operations by causing a downloader sync
	// until it was discovered they were invalid, whereon mining would resume.
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, true)

	mux.Post(downloader.FailedEvent{})
	waitForMiningState(t, beacon, true)
}

// TestBeaconDownloaderFirstFails tests that mining is only
// permitted to run indefinitely once the downloader sees a DoneEvent (success).
// An initial FailedEvent should allow mining to stop on a subsequent
// downloader StartEvent.
func TestBeaconDownloaderFirstFails(t *testing.T) {
	t.Parallel()
	beacon, mux, cleanup := createBeacon(t)
	defer cleanup(false)

	beacon.Start()
	waitForMiningState(t, beacon, true)
	// Start the downloader
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, false)

	// Stop the downloader and wait for the update loop to run
	mux.Post(downloader.FailedEvent{})
	waitForMiningState(t, beacon, true)

	// Since the downloader hasn't yet emitted a successful DoneEvent,
	// we expect the beacon to stop on next StartEvent.
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, false)

	// Downloader finally succeeds.
	mux.Post(downloader.DoneEvent{})
	waitForMiningState(t, beacon, true)

	// Downloader starts again.
	// Since it has achieved a DoneEvent once, we expect beacon
	// state to be unchanged.
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, true)

	mux.Post(downloader.FailedEvent{})
	waitForMiningState(t, beacon, true)
}

func TestBeaconStartStopAfterDownloaderEvents(t *testing.T) {
	t.Parallel()
	beacon, mux, cleanup := createBeacon(t)
	defer cleanup(false)

	beacon.Start()
	waitForMiningState(t, beacon, true)
	// Start the downloader
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, false)

	// Downloader finally succeeds.
	mux.Post(downloader.DoneEvent{})
	waitForMiningState(t, beacon, true)

	beacon.Stop()
	waitForMiningState(t, beacon, false)

	beacon.Start()
	waitForMiningState(t, beacon, true)

	beacon.Stop()
	waitForMiningState(t, beacon, false)
}

func TestStartWhileDownload(t *testing.T) {
	t.Parallel()
	beacon, mux, cleanup := createBeacon(t)
	defer cleanup(false)
	waitForMiningState(t, beacon, false)
	beacon.Start()
	waitForMiningState(t, beacon, true)
	// Stop the downloader and wait for the update loop to run
	mux.Post(downloader.StartEvent{})
	waitForMiningState(t, beacon, false)
	// Starting the beacon after the downloader should not work
	beacon.Start()
	waitForMiningState(t, beacon, false)
}

func TestStartStopBeacon(t *testing.T) {
	t.Parallel()
	beacon, _, cleanup := createBeacon(t)
	defer cleanup(false)
	waitForMiningState(t, beacon, false)
	beacon.Start()
	waitForMiningState(t, beacon, true)
	beacon.Stop()
	waitForMiningState(t, beacon, false)
}

func TestCloseBeacon(t *testing.T) {
	t.Parallel()
	beacon, _, cleanup := createBeacon(t)
	defer cleanup(true)
	waitForMiningState(t, beacon, false)
	beacon.Start()
	waitForMiningState(t, beacon, true)
	// Terminate the beacon and wait for the update loop to run
	beacon.Close()
	waitForMiningState(t, beacon, false)
}

// waitForMiningState waits until either
// * the desired mining state was reached
// * a timeout was reached which fails the test
func waitForMiningState(t *testing.T, m *Beacon, mining bool) {
	t.Helper()

	var state bool
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		if state = m.Mining(); state == mining {
			return
		}
	}
	t.Fatalf("Mining() == %t, want %t", state, mining)
}

func beaconTestGenesisBlock(period uint64, gasLimit uint64, faucet common.Address) *core.Genesis {
	config := *params.AllCliqueProtocolChanges
	config.Clique = &params.CliqueConfig{
		Period: period,
		Epoch:  config.Clique.Epoch,
	}

	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &core.Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]types.Account{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			common.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			faucet:                           {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}

func createBeacon(t *testing.T) (*Beacon, *event.TypeMux, func(skipBeacon bool)) {
	// Init coinbase
	etherbase := common.HexToAddress("123456789")
	// Create chainConfig
	chainDB := rawdb.NewMemoryDatabase()
	triedb := triedb.NewDatabase(chainDB, nil)
	genesis := beaconTestGenesisBlock(15, 11_500_000, common.HexToAddress("12345"))
	chainConfig, _, _, err := core.SetupGenesisBlock(chainDB, triedb, genesis)
	if err != nil {
		t.Fatalf("can't create new chain config: %v", err)
	}
	// Create consensus engine
	engine := clique.New(chainConfig.Clique, chainDB)
	// Create Ethereum backend
	bc, err := core.NewBlockChain(chainDB, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("can't create new chain %v", err)
	}

	// Create Beacon
	backend := NewMockBackend(bc)
	// Create event Mux
	mux := new(event.TypeMux)
	// Create Beacon
	beacon := New(backend, &rpc.Client{}, mux, engine, etherbase)
	cleanup := func(skipBeacon bool) {
		bc.Stop()
		engine.Close()
		if !skipBeacon {
			beacon.Close()
		}
	}
	return beacon, mux, cleanup
}
