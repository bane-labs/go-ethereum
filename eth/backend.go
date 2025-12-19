// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package eth implements the Ethereum protocol.
package eth

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/antimev"
	beaconImpl "github.com/ethereum/go-ethereum/beacon/impl"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/beacon"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/consensus/dbft"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/filesystem"
	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/core/filtermaps"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state/pruner"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/blobpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/txpool/locals"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	beaconproto "github.com/ethereum/go-ethereum/eth/protocols/beacon"
	dbftproto "github.com/ethereum/go-ethereum/eth/protocols/dbft"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/internal/shutdowncheck"
	"github.com/ethereum/go-ethereum/internal/version"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/miner"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/dnsdisc"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	gethversion "github.com/ethereum/go-ethereum/version"
)

// Config contains the configuration options of the ETH protocol.
// Deprecated: use ethconfig.Config instead.
type Config = ethconfig.Config

// Ethereum implements the Ethereum full node service.
type Ethereum struct {
	// core protocol objects
	config         *ethconfig.Config
	txPool         *txpool.TxPool
	localTxTracker *locals.TxTracker
	dbftSrv        *dbftproto.Service
	blockchain     *core.BlockChain
	filesystem     *core.FileSystem

	handler *handler
	discmix *enode.FairMix
	dropper *dropper

	// DB interfaces
	chainDb ethdb.Database // Block chain database

	eventMux        *event.TypeMux
	engine          consensus.Engine
	accountManager  *accounts.Manager
	antimevKeystore *antimev.KeyStore

	filterMaps      *filtermaps.FilterMaps
	closeFilterMaps chan chan struct{}

	APIBackend *EthAPIBackend

	internalRPC *rpc.Client
	beacon      *beaconImpl.Beacon

	miner        *miner.Miner
	gasPrice     *big.Int
	feeRecipient common.Address

	networkID     uint64
	netRPCService *ethapi.NetAPI

	p2pServer *p2p.Server

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

	shutdownTracker *shutdowncheck.ShutdownTracker // Tracks if and when the node has shutdown ungracefully
}

// New creates a new Ethereum object (including the initialisation of the common Ethereum object),
// whose lifecycle will be managed by the provided node.
func New(stack *node.Node, config *ethconfig.Config) (*Ethereum, error) {
	// Ensure configuration values are compatible and sane
	if !config.SyncMode.IsValid() {
		return nil, fmt.Errorf("invalid sync mode %d", config.SyncMode)
	}
	if !config.HistoryMode.IsValid() {
		return nil, fmt.Errorf("invalid history mode %d", config.HistoryMode)
	}
	if config.Miner.GasPrice == nil || config.Miner.GasPrice.Sign() <= 0 {
		log.Warn("Sanitizing invalid miner gas price", "provided", config.Miner.GasPrice, "updated", ethconfig.Defaults.Miner.GasPrice)
		config.Miner.GasPrice = new(big.Int).Set(ethconfig.Defaults.Miner.GasPrice)
	}
	if config.NoPruning && config.TrieDirtyCache > 0 {
		if config.SnapshotCache > 0 {
			config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
			config.SnapshotCache += config.TrieDirtyCache * 2 / 5
		} else {
			config.TrieCleanCache += config.TrieDirtyCache
		}
		config.TrieDirtyCache = 0
	}
	log.Info("Allocated trie memory caches", "clean", common.StorageSize(config.TrieCleanCache)*1024*1024, "dirty", common.StorageSize(config.TrieDirtyCache)*1024*1024)

	chainDb, err := stack.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "eth/db/chaindata/", false)
	if err != nil {
		return nil, err
	}
	scheme, err := rawdb.ParseStateScheme(config.StateScheme, chainDb)
	if err != nil {
		return nil, err
	}
	// Try to recover offline state pruning only in hash-based.
	if scheme == rawdb.HashScheme {
		if err := pruner.RecoverPruning(stack.ResolvePath(""), chainDb); err != nil {
			log.Error("Failed to recover state", "error", err)
		}
	}

	// Here we determine genesis hash and active ChainConfig.
	// We need these to figure out the consensus parameters and to set up history pruning.
	chainConfig, _, err := core.LoadChainConfig(chainDb, config.Genesis)
	if err != nil {
		return nil, err
	}
	engine, err := ethconfig.CreateConsensusEngine(chainConfig, chainDb, stack.Config().DBFTStatistics)
	if err != nil {
		return nil, err
	}
	// Set networkID to chainID by default.
	networkID := config.NetworkId
	if networkID == 0 {
		networkID = chainConfig.ChainID.Uint64()
	}

	// Assemble the Ethereum object.
	eth := &Ethereum{
		config:          config,
		chainDb:         chainDb,
		eventMux:        stack.EventMux(),
		accountManager:  stack.AccountManager(),
		engine:          engine,
		networkID:       networkID,
		gasPrice:        config.Miner.GasPrice,
		feeRecipient:    config.Miner.PendingFeeRecipient,
		p2pServer:       stack.Server(),
		discmix:         enode.NewFairMix(0),
		shutdownTracker: shutdowncheck.NewShutdownTracker(chainDb),
	}
	eth.antimevKeystore = stack.AntiMEVKeyStore()
	// Override the address that mining rewards will be sent to.
	if chainConfig.DBFT != nil {
		eth.feeRecipient = chainConfig.DBFT.Coinbase
	}
	bcVersion := rawdb.ReadDatabaseVersion(chainDb)
	var dbVer = "<nil>"
	if bcVersion != nil {
		dbVer = fmt.Sprintf("%d", *bcVersion)
	}
	log.Info("Initialising Ethereum protocol", "network", networkID, "dbversion", dbVer)

	// Create BlockChain object.
	if !config.SkipBcVersionCheck {
		if bcVersion != nil && *bcVersion > core.BlockChainVersion {
			return nil, fmt.Errorf("database version is v%d, Geth %s only supports v%d", *bcVersion, version.WithMeta, core.BlockChainVersion)
		} else if bcVersion == nil || *bcVersion < core.BlockChainVersion {
			if bcVersion != nil { // only print warning on upgrade, not on init
				log.Warn("Upgrade blockchain database version", "from", dbVer, "to", core.BlockChainVersion)
			}
			rawdb.WriteDatabaseVersion(chainDb, core.BlockChainVersion)
		}
	}
	var (
		vmConfig = vm.Config{
			EnablePreimageRecording: config.EnablePreimageRecording,
		}
		cacheConfig = &core.CacheConfig{
			TrieCleanLimit:      config.TrieCleanCache,
			TrieCleanNoPrefetch: config.NoPrefetch,
			TrieDirtyLimit:      config.TrieDirtyCache,
			TrieDirtyDisabled:   config.NoPruning,
			TrieTimeLimit:       config.TrieTimeout,
			SnapshotLimit:       config.SnapshotCache,
			Preimages:           config.Preimages,
			StateHistory:        config.StateHistory,
			StateScheme:         scheme,
			ChainHistoryMode:    config.HistoryMode,
		}
	)
	if config.VMTrace != "" {
		traceConfig := json.RawMessage("{}")
		if config.VMTraceJsonConfig != "" {
			traceConfig = json.RawMessage(config.VMTraceJsonConfig)
		}
		t, err := tracers.LiveDirectory.New(config.VMTrace, traceConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create tracer %s: %v", config.VMTrace, err)
		}
		vmConfig.Tracer = t
	}
	// Override the chain config with provided settings.
	var overrides core.ChainOverrides
	if config.OverridePrague != nil {
		overrides.OverridePrague = config.OverridePrague
	}
	if config.OverrideVerkle != nil {
		overrides.OverrideVerkle = config.OverrideVerkle
	}
	eth.blockchain, err = core.NewBlockChain(chainDb, cacheConfig, config.Genesis, &overrides, eth.engine, vmConfig, &config.TransactionHistory)
	if err != nil {
		return nil, err
	}

	// Initialize filtermaps log index.
	fmConfig := filtermaps.Config{
		History:        config.LogHistory,
		Disabled:       config.LogNoHistory,
		ExportFileName: config.LogExportCheckpoints,
		HashScheme:     scheme == rawdb.HashScheme,
	}
	chainView := eth.newChainView(eth.blockchain.CurrentBlock())
	historyCutoff, _ := eth.blockchain.HistoryPruningCutoff()
	var finalBlock uint64
	if fb := eth.blockchain.CurrentFinalBlock(); fb != nil {
		finalBlock = fb.Number.Uint64()
	}
	eth.filterMaps = filtermaps.NewFilterMaps(chainDb, chainView, historyCutoff, finalBlock, filtermaps.DefaultParams, fmConfig)
	eth.closeFilterMaps = make(chan chan struct{})

	// TxPool
	if config.TxPool.Journal != "" {
		config.TxPool.Journal = stack.ResolvePath(config.TxPool.Journal)
	}
	legacyPool := legacypool.New(config.TxPool, eth.blockchain)

	if config.BlobPool.Datadir != "" {
		config.BlobPool.Datadir = stack.ResolvePath(config.BlobPool.Datadir)
	}
	blobPool := blobpool.New(config.BlobPool, eth.blockchain, legacyPool.HasPendingAuth)
	subPools := []txpool.SubPool{legacyPool, blobPool}
	enableAMEVCachePool := config.TxPool.AMEVCache && !config.TxPool.NoLocals
	if enableAMEVCachePool {
		cfg := legacypool.CacheConfig{
			AccountSlots: config.TxPool.AccountSlots,
			GlobalSlots:  config.TxPool.GlobalSlots,
			Lifetime:     config.TxPool.Lifetime,
		}
		cachePool := legacypool.NewCache(cfg, eth.blockchain)
		subPools = append([]txpool.SubPool{cachePool}, subPools...)
	}

	eth.txPool, err = txpool.New(config.TxPool.PriceLimit, eth.blockchain, subPools)
	if err != nil {
		return nil, err
	}

	if !config.TxPool.NoLocals {
		rejournal := config.TxPool.Rejournal
		if rejournal < time.Second {
			log.Warn("Sanitizing invalid txpool journal time", "provided", rejournal, "updated", time.Second)
			rejournal = time.Second
		}
		eth.localTxTracker = locals.New(config.TxPool.Journal, rejournal, eth.blockchain.Config(), eth.txPool)
		stack.RegisterLifecycle(eth.localTxTracker)
	}

	blobStorage, err := filesystem.NewBlobStorage(
		filesystem.WithBlobRetentionEpochs(primitives.Epoch(stack.Config().BlobRetentionEpoch)),
		filesystem.WithBasePath(stack.Config().BlobStoragePath),
		filesystem.WithLayout(stack.Config().BlobStorageLayout), // This is validated in the Action func for BlobStorageLayout.
		filesystem.WithSaveFsync(stack.Config().BlobSaveFsync),
		filesystem.WithChainConfig(chainConfig),
	)
	if err != nil {
		return nil, err
	}
	blobStorage.WarmCache()
	eth.filesystem, err = core.NewFileSystem(eth.blockchain, blobPool, blobStorage)
	if err != nil {
		return nil, err
	}

	// Permit the downloader to use the trie cache allowance during fast sync
	cacheLimit := cacheConfig.TrieCleanLimit + cacheConfig.TrieDirtyLimit + cacheConfig.SnapshotLimit
	if eth.handler, err = newHandler(&handlerConfig{
		NodeID:         eth.p2pServer.Self().ID(),
		Database:       chainDb,
		Chain:          eth.blockchain,
		TxPool:         eth.txPool,
		Network:        networkID,
		Sync:           config.SyncMode,
		BloomCache:     uint64(cacheLimit),
		EventMux:       eth.eventMux,
		RequiredBlocks: config.RequiredBlocks,
		FileSystem:     eth.filesystem,
		NoPruning:      config.NoPruning,
	}); err != nil {
		return nil, err
	}

	eth.dropper = newDropper(eth.p2pServer.MaxDialedConns(), eth.p2pServer.MaxInboundConns())

	eth.miner = miner.New(eth, config.Miner, eth.engine)
	if chainConfig.DBFT != nil && len(config.Miner.ExtraData) != 0 {
		return nil, fmt.Errorf("custom miner Extra is not supported by dBFT")
	}
	eth.miner.SetExtra(makeExtraData(config.Miner.ExtraData, chainConfig.DBFT != nil))
	eth.miner.SetPrioAddresses(config.TxPool.Locals)

	eth.APIBackend = &EthAPIBackend{stack.Config().ExtRPCEnabled(), stack.Config().AllowUnprotectedTxs, eth, nil}
	if eth.APIBackend.allowUnprotectedTxs {
		log.Info("Unprotected transactions allowed")
	}
	eth.APIBackend.gpo = gasprice.NewOracle(eth.APIBackend, config.GPO, config.Miner.GasPrice)

	var (
		bft                 *dbft.DBFT
		onPayload           func(*dbftproto.Message) error
		isExtensibleAllowed func(uint64, common.Address) error
	)
	switch t := eth.engine.(type) {
	case *dbft.DBFT:
		bft = t
	case *beacon.Beacon:
		switch inner := t.InnerEngine().(type) {
		case *dbft.DBFT:
			bft = inner
		}
	}
	if bft != nil {
		onPayload = bft.OnPayload
		isExtensibleAllowed = bft.IsExtensibleAllowed
	}
	eth.dbftSrv = dbftproto.New(ethapi.NewBlockChainAPI(eth.APIBackend), onPayload, isExtensibleAllowed)
	if bft != nil {
		bft.WithEthAPIBackend(eth.APIBackend)
		bft.WithBroadcast(eth.dbftSrv.BroadcastMessage)
		bft.WithTxPool(eth.TxPool())
		bft.WithRequestTxs(eth.handler.BroadcastRequestTxs)
		err := bft.WithLogLevel(config.DBFTLogLevel)
		if err != nil {
			return nil, err
		}
		if config.AntiMEVEnforceECDSABlockSignatureScheme {
			bft.EnforceECDSASignatures()
		}
	}

	// Start the RPC service
	eth.netRPCService = ethapi.NewNetAPI(eth.p2pServer, networkID)

	// Register the backend on the node
	stack.RegisterAPIs(eth.APIs())
	stack.RegisterProtocols(eth.Protocols())
	stack.RegisterLifecycle(eth)

	// Set up local beacon client
	eth.internalRPC = stack.Attach()
	eth.beacon = beaconImpl.New(eth, eth.internalRPC, eth.EventMux(), eth.feeRecipient, eth.shouldPreserve)
	eth.handler.connectBeacon(eth.beacon)
	if bft != nil {
		// Connect BFT to beacon protocol
		bft.WithBeacon(eth.beacon.BlockBroadcaster(), eth.beacon.SubscribeSyncingEvents, eth.beacon.Syncing)
	}

	// Successful startup; push a marker and check previous unclean shutdowns.
	eth.shutdownTracker.MarkStartup()

	return eth, nil
}

func makeExtraData(extra []byte, isBFT bool) []byte {
	if len(extra) == 0 {
		// create default extradata
		if isBFT {
			// Will be filled by consensus engine.
			extra = []byte{}
		} else {
			extra, _ = rlp.EncodeToBytes([]interface{}{
				uint(gethversion.Major<<16 | gethversion.Minor<<8 | gethversion.Patch),
				"geth",
				runtime.Version(),
				runtime.GOOS,
			})
		}
	}
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		log.Warn("Miner extra data exceed limit", "extra", hexutil.Bytes(extra), "limit", params.MaximumExtraDataSize)
		extra = nil
	}
	return extra
}

// APIs return the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Ethereum) APIs() []rpc.API {
	apis := ethapi.GetAPIs(s.APIBackend)

	// Append any APIs exposed explicitly by the consensus engine
	apis = append(apis, s.engine.APIs(s.BlockChain())...)

	// Append all the local APIs and return
	return append(apis, []rpc.API{
		{
			Namespace: "miner",
			Service:   NewMinerAPI(s),
		}, {
			Namespace: "eth",
			Service:   downloader.NewDownloaderAPI(s.handler.downloader, s.blockchain, s.eventMux),
		}, {
			Namespace: "admin",
			Service:   NewAdminAPI(s),
		}, {
			Namespace: "debug",
			Service:   NewDebugAPI(s),
		}, {
			Namespace: "net",
			Service:   s.netRPCService,
		},
	}...)
}

func (s *Ethereum) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

// isLocalBlock checks whether the specified block is mined
// by local miner accounts.
//
// We regard two types of accounts as local miner account: etherbase
// and accounts specified via `txpool.locals` flag.
func (s *Ethereum) isLocalBlock(header *types.Header) bool {
	author, err := s.engine.Author(header)
	if err != nil {
		log.Warn("Failed to retrieve block author", "number", header.Number.Uint64(), "hash", header.Hash(), "err", err)
		return false
	}
	// Check whether the given address is etherbase.
	s.lock.RLock()
	feeRecipient := s.feeRecipient
	s.lock.RUnlock()
	if author == feeRecipient {
		return true
	}
	// Check whether the given address is specified by `txpool.local`
	// CLI flag.
	for _, account := range s.config.TxPool.Locals {
		if account == author {
			return true
		}
	}
	return false
}

// shouldPreserve checks whether we should preserve the given block
// during the chain reorg depending on whether the author of block
// is a local account.
func (s *Ethereum) shouldPreserve(header *types.Header) bool {
	// The reason we need to disable the self-reorg preserving for clique
	// is it can be probable to introduce a deadlock.
	//
	// e.g. If there are 7 available signers
	//
	// r1   A
	// r2     B
	// r3       C
	// r4         D
	// r5   A      [X] F G
	// r6    [X]
	//
	// In the round5, the in-turn signer E is offline, so the worst case
	// is A, F and G sign the block of round5 and reject the block of opponents
	// and in the round6, the last available signer B is offline, the whole
	// network is stuck.
	if _, ok := s.engine.(*clique.Clique); ok {
		return false
	}
	if _, ok := s.engine.(*dbft.DBFT); ok {
		return false
	}
	return s.isLocalBlock(header)
}

// StartMining starts the miner with the given number of CPU threads. If mining
// is already running, this method adjust the number of threads allowed to use
// and updates the minimum price required by the transaction pool.
func (s *Ethereum) StartMining() error {
	// If the miner was not running, initialize it
	if !s.beacon.Mining() {
		// Propagate the initial price point to the transaction pool
		s.lock.RLock()
		price := s.gasPrice
		s.lock.RUnlock()
		s.txPool.SetGasTip(price)

		// Configure the local mining address
		eb := s.config.Miner.PendingFeeRecipient
		if eb == (common.Address{}) {
			err := errors.New("etherbase must be explicitly specified")
			log.Error("Cannot start mining without etherbase", "err", err)
			return fmt.Errorf("etherbase missing: %v", err)
		}
		var (
			bft *dbft.DBFT
		)
		switch t := s.engine.(type) {
		case *dbft.DBFT:
			bft = t
		case *beacon.Beacon:
			switch inner := t.InnerEngine().(type) {
			case *dbft.DBFT:
				bft = inner
			}
		}
		if bft != nil {
			wallet, err := s.accountManager.Find(accounts.Account{Address: eb})
			if wallet == nil || err != nil {
				log.Error("Etherbase account unavailable locally", "err", err)
				return fmt.Errorf("signer missing: %v", err)
			}
			dkgIdentity := s.antimevKeystore.Address()
			if eb != dkgIdentity {
				log.Error("Antimev keystore mismatch", "consensus address", eb, "keystore", dkgIdentity)
				return fmt.Errorf("keystore address mismatch: %v", dkgIdentity)
			}
			log.Info("Initializing BFT consensus",
				"account", eb.String())
			bft.Authorize(eb, wallet.SignData, s.antimevKeystore)
			err = bft.WithZKFiles(
				s.config.OneMsgR1CSPath,
				s.config.TwoMsgR1CSPath,
				s.config.SevenMsgR1CSPath,
				s.config.OneMsgProvingKeyPath,
				s.config.TwoMsgProvingKeyPath,
				s.config.SevenMsgProvingKeyPath,
			)
			if err != nil {
				return fmt.Errorf("DKG initialization failed: %v", err)
			}
		}
		// If mining is started, we can disable the transaction rejection mechanism
		// introduced to speed sync times.
		s.handler.enableSyncedFeatures()

		go s.beacon.StartMining()
		if bft != nil {
			go bft.Start(s.blockchain, s.beacon.InsertBlock, s.filesystem)
		}
	}
	return nil
}

// StopMining terminates the miner, both at the consensus engine level as well as
// at the block creation level.
func (s *Ethereum) StopMining() {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		th.SetThreads(-1)
	}
	// Stop the block creating itself
	s.beacon.StopMining()
}

func (s *Ethereum) Miner() *miner.Miner { return s.miner }

func (s *Ethereum) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Ethereum) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *Ethereum) FileSystem() *core.FileSystem       { return s.filesystem }
func (s *Ethereum) TxPool() *txpool.TxPool             { return s.txPool }
func (s *Ethereum) EventMux() *event.TypeMux           { return s.eventMux }
func (s *Ethereum) Engine() consensus.Engine           { return s.engine }
func (s *Ethereum) ChainDb() ethdb.Database            { return s.chainDb }
func (s *Ethereum) IsListening() bool                  { return true } // Always listening
func (s *Ethereum) Downloader() *downloader.Downloader { return s.handler.downloader }
func (s *Ethereum) Synced() bool                       { return s.handler.synced.Load() }
func (s *Ethereum) SetSynced()                         { s.handler.enableSyncedFeatures() }
func (s *Ethereum) ArchiveMode() bool                  { return s.config.NoPruning }
func (s *Ethereum) SyncMode() ethconfig.SyncMode {
	mode, _ := s.handler.chainSync.modeAndLocalHead()
	return mode
}

// Protocols returns all the currently configured
// network protocols to start.
func (s *Ethereum) Protocols() []p2p.Protocol {
	protos := beaconproto.MakeProtocols((*beaconHandler)(s.handler))
	protos = append(protos, eth.MakeProtocols((*ethHandler)(s.handler), s.networkID, s.discmix)...)
	if s.config.SnapshotCache > 0 {
		protos = append(protos, snap.MakeProtocols((*snapHandler)(s.handler))...)
	}
	protos = append(protos, s.dbftSrv.MakeProtocols()...)
	return protos
}

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// Ethereum protocol implementation.
func (s *Ethereum) Start() error {
	if err := s.setupDiscovery(); err != nil {
		return err
	}

	// Regularly update shutdown marker
	s.shutdownTracker.Start()

	// Start the networking layer
	s.handler.Start(s.p2pServer.MaxPeers)

	// Start the connection manager
	s.dropper.Start(s.p2pServer, func() bool { return !s.Synced() })

	// start log indexer
	s.filterMaps.Start()
	go s.updateFilterMapsHeads()
	return nil
}

func (s *Ethereum) newChainView(head *types.Header) *filtermaps.ChainView {
	if head == nil {
		return nil
	}
	return filtermaps.NewChainView(s.blockchain, head.Number.Uint64(), head.Hash())
}

func (s *Ethereum) updateFilterMapsHeads() {
	headEventCh := make(chan core.ChainEvent, 10)
	blockProcCh := make(chan bool, 10)
	sub := s.blockchain.SubscribeChainEvent(headEventCh)
	sub2 := s.blockchain.SubscribeBlockProcessingEvent(blockProcCh)
	defer func() {
		sub.Unsubscribe()
		sub2.Unsubscribe()
		for {
			select {
			case <-headEventCh:
			case <-blockProcCh:
			default:
				return
			}
		}
	}()

	var head *types.Header
	setHead := func(newHead *types.Header) {
		if newHead == nil {
			return
		}
		if head == nil || newHead.Hash() != head.Hash() {
			head = newHead
			chainView := s.newChainView(head)
			historyCutoff, _ := s.blockchain.HistoryPruningCutoff()
			var finalBlock uint64
			if fb := s.blockchain.CurrentFinalBlock(); fb != nil {
				finalBlock = fb.Number.Uint64()
			}
			s.filterMaps.SetTarget(chainView, historyCutoff, finalBlock)
		}
	}
	setHead(s.blockchain.CurrentBlock())

	for {
		select {
		case ev := <-headEventCh:
			setHead(ev.Header)
		case blockProc := <-blockProcCh:
			s.filterMaps.SetBlockProcessing(blockProc)
		case <-time.After(time.Second * 10):
			setHead(s.blockchain.CurrentBlock())
		case ch := <-s.closeFilterMaps:
			close(ch)
			return
		}
	}
}

func (s *Ethereum) setupDiscovery() error {
	eth.StartENRUpdater(s.blockchain, s.p2pServer.LocalNode())

	// Add eth nodes from DNS.
	dnsclient := dnsdisc.NewClient(dnsdisc.Config{})
	if len(s.config.EthDiscoveryURLs) > 0 {
		iter, err := dnsclient.NewIterator(s.config.EthDiscoveryURLs...)
		if err != nil {
			return err
		}
		s.discmix.AddSource(iter)
	}

	// Add snap nodes from DNS.
	if len(s.config.SnapDiscoveryURLs) > 0 {
		iter, err := dnsclient.NewIterator(s.config.SnapDiscoveryURLs...)
		if err != nil {
			return err
		}
		s.discmix.AddSource(iter)
	}

	// Add DHT nodes from discv5.
	if s.p2pServer.DiscoveryV5() != nil {
		filter := eth.NewNodeFilter(s.blockchain)
		iter := enode.Filter(s.p2pServer.DiscoveryV5().RandomNodes(), filter)
		s.discmix.AddSource(iter)
	}

	return nil
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *Ethereum) Stop() error {
	// Stop all the peer-related stuff first.
	s.discmix.Close()
	s.dropper.Stop()
	s.handler.Stop()

	// Then stop everything else.
	ch := make(chan struct{})
	s.closeFilterMaps <- ch
	<-ch
	s.filterMaps.Stop()
	s.txPool.Close()
	s.beacon.Close()
	s.internalRPC.Close()
	s.blockchain.Stop()
	s.engine.Close()

	// Clean shutdown marker as the last thing before closing db
	s.shutdownTracker.Stop()

	s.chainDb.Close()
	s.eventMux.Stop()

	return nil
}
