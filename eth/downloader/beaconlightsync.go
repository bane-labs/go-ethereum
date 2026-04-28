package downloader

import (
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/beacon/impl/synchronizer"
	beaconSync "github.com/ethereum/go-ethereum/beacon/impl/synchronizer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/dbft/light"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// BeaconLightSync is a light client protocol for only dBFT to verify the beacon
// sync target header by hash. This grants the consensus layer the ability to
// verify the beacon sync target header without executing the chain history, but
// only checking the signatures against the NextConsensus specification.
func (d *Downloader) BeaconLightSync(extend beaconSync.BeaconExtendFn, start chan *types.Header) error {
	var trustedHeader *types.Header
	var shouldSync atomic.Bool
	for {
		// If the node is going down, unblock
		select {
		case header, ok := <-start:
			if !ok {
				return nil
			}
			if !shouldSync.Load() {
				trustedHeader = header
				shouldSync.Store(true)
			}
			if trustedHeader == nil || trustedHeader.Number.Uint64() < header.Number.Uint64() {
				trustedHeader = header
			}
		default:
		}
		// Do nothing if should wait
		if !shouldSync.Load() {
			time.Sleep(time.Second * 3)
			continue
		}
		// Pick a random peer to sync from and keep retrying if none are yet
		// available due to fresh startup
		d.peers.lock.RLock()
		var peer *peerConnection
		for _, peer = range d.peers.peers {
			break
		}
		d.peers.lock.RUnlock()

		if peer == nil {
			time.Sleep(time.Second)
			continue
		}
		// Found a peer, attempt to retrieve the header whilst blocking and
		// retry if it fails for whatever reason
		log.Debug("Attempting to retrieve new headers", "peer", peer.id)
		headers, metas, err := d.fetchHeadersByHash(peer, trustedHeader.Hash(), synchronizer.ExpectedHeadersNum, 0, false)
		if err != nil {
			// If downloader is canceled, then abort and wait for a new start
			if err == errCanceled {
				shouldSync.Store(false)
				continue
			}
			log.Debug("Failed to fetch new headers", "err", err)
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		if len(headers) == 0 {
			log.Debug("Received empty new headers")
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		// Head header retrieved, if the hash matches, start the verification
		if metas[0] != trustedHeader.Hash() {
			log.Debug("Received invalid new headers start", "want", trustedHeader.Hash(), "have", metas[0])
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		// If there's no new headers to sync
		if len(metas) < 2 {
			// Notify the synchronizer nothing to extend, and wait for a new start signal to recover
			extend(headers, metas, nil, nil)
			shouldSync.Store(false)
			continue
		}
		// Verifiy based on our light client rules, if it fails, retry
		valid := light.VerifyHeaders(headers)
		if !valid {
			log.Debug("Received invalid new headers", "start", trustedHeader.Hash(), "end", headers[len(headers)-1].Hash())
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		// If the verification is successful, update the trusted hash and repeat until
		// the light chain is extended. Here we take n-1, since n may get reorg
		trusted := headers[len(headers)-2]
		latest := headers[len(headers)-1]
		bodies, _, err := d.fetchBodiesByHash(peer, []common.Hash{trusted.Hash(), latest.Hash()})
		if err != nil {
			// If downloader is canceled, then abort and wait for a new start
			if err == errCanceled {
				shouldSync.Store(false)
				continue
			}
			log.Debug("Failed to fetch new bodies", "err", err)
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		if len(bodies) != 2 {
			log.Debug("Received invalid number of new bodies", "len", len(bodies))
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}
		// Verify the bodies match the headers, if not, retry
		// Rebuild the block trusted to be finalized
		body := types.Body{
			Transactions: bodies[0].Transactions,
			Uncles:       bodies[0].Uncles,
			Withdrawals:  bodies[0].Withdrawals,
		}
		finalizedBlock := types.NewBlockWithHeader(trusted).WithBody(body)
		// Rebuild the block temporarily latest
		body = types.Body{
			Transactions: bodies[1].Transactions,
			Uncles:       bodies[1].Uncles,
			Withdrawals:  bodies[1].Withdrawals,
		}
		latestBlock := types.NewBlockWithHeader(latest).WithBody(body)
		if finalizedBlock.Hash() != trusted.Hash() || latestBlock.Hash() != latest.Hash() {
			log.Debug("Received invalid new bodies", "trusted", trusted.Hash(), "latest", latest.Hash())
			d.dropPeer(peer.id)
			time.Sleep(time.Second)
			continue
		}

		log.Debug("Successfully fetch light headers", "start", trustedHeader.Hash(), "blocks", len(metas))
		trustedHeader = trusted
		// Try to extend the chain trusted to pending headers from the network
		if err := extend(headers, metas, finalizedBlock, latestBlock); err != nil {
			// Wait for a new start signal to recover
			shouldSync.Store(false)
			continue
		}
		// If there's no new headers to handle
		if len(headers) < synchronizer.ExpectedHeadersNum {
			shouldSync.Store(false)
		}
	}
}
