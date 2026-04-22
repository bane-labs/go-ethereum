// Copyright 2020 The go-ethereum Authors
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

package eth

import (
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/eth/protocols/beacon"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/p2p"
)

var (
	// errPeerSetClosed is returned if a peer is attempted to be added or removed
	// from the peer set after it has been terminated.
	errPeerSetClosed = errors.New("peerset closed")

	// errPeerAlreadyRegistered is returned if a peer is attempted to be added
	// to the peer set, but one with the same id already exists.
	errPeerAlreadyRegistered = errors.New("peer already registered")

	// errPeerNotRegistered is returned if a peer is attempted to be removed from
	// a peer set, but no peer with the given id exists.
	errPeerNotRegistered = errors.New("peer not registered")

	// errSnapWithoutEth is returned if a peer attempts to connect only on the
	// snap protocol without advertising the eth main protocol.
	errSnapWithoutEth = errors.New("peer connected on snap without compatible eth support")
)

// peerSet represents the collection of active peers currently participating in
// the `eth` protocol, with or without the `snap` extension.
type peerSet struct {
	beacons   map[string]*beaconPeer // Peers connected on the `beacon` protocol
	peers     map[string]*ethPeer    // Peers connected on the `eth` protocol
	snapPeers int                    // Number of `snap` compatible peers for connection prioritization

	snapWait map[string]chan *snap.Peer // Peers connected on `eth` waiting for their snap extension
	snapPend map[string]*snap.Peer      // Peers connected on the `snap` protocol, but not yet on `eth`

	noBlobPeers map[string]time.Time // Peers that have not been able to provide blobs recently

	lock   sync.RWMutex
	closed bool
	quitCh chan struct{} // Quit channel to signal termination
}

// newPeerSet creates a new peer set to track the active participants.
func newPeerSet() *peerSet {
	return &peerSet{
		beacons:     make(map[string]*beaconPeer),
		peers:       make(map[string]*ethPeer),
		snapWait:    make(map[string]chan *snap.Peer),
		snapPend:    make(map[string]*snap.Peer),
		noBlobPeers: make(map[string]time.Time),
		quitCh:      make(chan struct{}),
	}
}

// registerSnapExtension unblocks an already connected `eth` peer waiting for its
// `snap` extension, or if no such peer exists, tracks the extension for the time
// being until the `eth` main protocol starts looking for it.
func (ps *peerSet) registerSnapExtension(peer *snap.Peer) error {
	// Reject the peer if it advertises `snap` without `eth` as `snap` is only a
	// satellite protocol meaningful with the chain selection of `eth`
	if !peer.RunningCap(eth.ProtocolName, eth.ProtocolVersions) {
		return fmt.Errorf("%w: have %v", errSnapWithoutEth, peer.Caps())
	}
	// Ensure nobody can double connect
	ps.lock.Lock()
	defer ps.lock.Unlock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.snapPend[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// Inject the peer into an `eth` counterpart is available, otherwise save for later
	if wait, ok := ps.snapWait[id]; ok {
		delete(ps.snapWait, id)
		wait <- peer
		return nil
	}
	ps.snapPend[id] = peer
	return nil
}

// waitSnapExtension blocks until all satellite protocols are connected and tracked
// by the peerset.
func (ps *peerSet) waitSnapExtension(peer *eth.Peer) (*snap.Peer, error) {
	// If the peer does not support a compatible `snap`, don't wait
	if !peer.RunningCap(snap.ProtocolName, snap.ProtocolVersions) {
		return nil, nil
	}
	// Ensure nobody can double connect
	ps.lock.Lock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.snapWait[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// If `snap` already connected, retrieve the peer from the pending set
	if snap, ok := ps.snapPend[id]; ok {
		delete(ps.snapPend, id)

		ps.lock.Unlock()
		return snap, nil
	}
	// Otherwise wait for `snap` to connect concurrently
	wait := make(chan *snap.Peer)
	ps.snapWait[id] = wait
	ps.lock.Unlock()

	select {
	case p := <-wait:
		return p, nil
	case <-ps.quitCh:
		ps.lock.Lock()
		delete(ps.snapWait, id)
		ps.lock.Unlock()
		return nil, errPeerSetClosed
	}
}

// registerBeacon injects a new `beacon` peer into the working set, or returns an error
// if the peer is already known.
func (ps *peerSet) registerBeacon(peer *beacon.Peer) error {
	// Start tracking the new peer
	ps.lock.Lock()
	defer ps.lock.Unlock()

	if ps.closed {
		return errPeerSetClosed
	}
	id := peer.ID()
	if _, ok := ps.beacons[id]; ok {
		return errPeerAlreadyRegistered
	}
	beacon := &beaconPeer{
		Peer: peer,
	}
	ps.beacons[id] = beacon
	return nil
}

// registerPeer injects a new `eth` peer into the working set, or returns an error
// if the peer is already known.
func (ps *peerSet) registerPeer(peer *eth.Peer, ext *snap.Peer) error {
	// Start tracking the new peer
	ps.lock.Lock()
	defer ps.lock.Unlock()

	if ps.closed {
		return errPeerSetClosed
	}
	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		return errPeerAlreadyRegistered
	}
	eth := &ethPeer{
		Peer: peer,
	}
	if ext != nil {
		eth.snapExt = &snapPeer{ext}
		ps.snapPeers++
	}
	ps.peers[id] = eth
	return nil
}

// unregisterPeer removes a remote peer from the active set, disabling any further
// actions to/from that particular entity.
func (ps *peerSet) unregisterPeer(id string) error {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	peer, ok := ps.peers[id]
	if !ok {
		return errPeerNotRegistered
	}
	delete(ps.peers, id)
	if peer.snapExt != nil {
		ps.snapPeers--
	}
	return nil
}

// unregisterBeacon removes a remote beacon peer from the active set, disabling any
// further actions to/from that particular entity.
func (ps *peerSet) unregisterBeacon(id string) error {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	_, ok := ps.beacons[id]
	if !ok {
		return errPeerNotRegistered
	}
	delete(ps.beacons, id)
	return nil
}

// beacon retrieves the registered beacon peer with the given id.
func (ps *peerSet) beacon(id string) *beaconPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.beacons[id]
}

// peer retrieves the registered peer with the given id.
func (ps *peerSet) peer(id string) *ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.peers[id]
}

// beaconsWithoutBlock retrieves a list of peers that do not have a given block in
// their set of known hashes so it might be propagated to them.
func (ps *peerSet) beaconsWithoutBlock(hash common.Hash) []*beaconPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*beaconPeer, 0, len(ps.beacons))
	for _, b := range ps.beacons {
		if !b.KnownBlock(hash) {
			list = append(list, b)
		}
	}
	return list
}

// beaconsWithoutBlockBlobs retrieves a list of peers that do not have a given blob in
// their set of known hashes.
func (ps *peerSet) beaconsWithoutBlockBlobs(hash common.Hash) []*beaconPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*beaconPeer, 0, len(ps.beacons))
	for _, p := range ps.beacons {
		if !p.KnownBlockBlobs(hash) {
			list = append(list, p)
		}
	}
	return list
}

// all returns all current peers.
func (ps *peerSet) all() []*ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return slices.Collect(maps.Values(ps.peers))
}

// allBeacons retrieves all of the beacon peers.
func (ps *peerSet) allBeacons() []*beaconPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return slices.Collect(maps.Values(ps.beacons))
}

// headPeers retrieves a specified number list of peers.
func (ps *peerSet) headPeers(num uint) []*ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	if num > uint(len(ps.peers)) {
		num = uint(len(ps.peers))
	}

	list := make([]*ethPeer, 0, num)
	for _, p := range ps.peers {
		if len(list) > int(num) {
			break
		}
		list = append(list, p)
	}
	return list
}

// len returns if the current number of `eth` peers in the set. Since the `snap`
// peers are tied to the existence of an `eth` connection, that will always be a
// subset of `eth`.
func (ps *peerSet) len() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return len(ps.peers)
}

// beaconLen returns if the current number of `beacon` peers in the set.
func (ps *peerSet) beaconLen() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return len(ps.beacons)
}

// snapLen returns if the current number of `snap` peers in the set.
func (ps *peerSet) snapLen() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.snapPeers
}

// peerWithHighestTD retrieves the known peer with the currently highest total
// difficulty, but below the given PoS switchover threshold.
func (ps *peerSet) peerWithHighestTD() (*beacon.Peer, *eth.Peer) {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	var (
		bestBeacon *beacon.Peer
		bestPeer   *eth.Peer
		bestTd     *big.Int
	)
	for _, b := range ps.beacons {
		if _, td := b.Head(); bestPeer == nil || td.Cmp(bestTd) > 0 {
			if p := ps.peers[b.ID()]; p != nil {
				bestBeacon, bestPeer, bestTd = b.Peer, p.Peer, td
			}
		}
	}
	return bestBeacon, bestPeer
}

// close disconnects all peers.
func (ps *peerSet) close() {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	for _, p := range ps.peers {
		p.Disconnect(p2p.DiscQuitting)
	}
	for _, b := range ps.beacons {
		b.Disconnect(p2p.DiscQuitting)
	}
	if !ps.closed {
		close(ps.quitCh)
	}
	ps.closed = true
}

// markNoBlobPeer marks a peer as not having blobs.
func (ps *peerSet) markNoBlobPeer(id string) {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	ps.noBlobPeers[id] = time.Now()
}

// blobPeers retrieves all of the filtered blob peers.
func (ps *peerSet) blobPeers() []string {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	// Filter out peers that have not been able to obtain the blob recently.
	now := time.Now()
	filtered := make([]string, 0, len(ps.beacons))
	for id := range ps.beacons {
		if timeout, ok := ps.noBlobPeers[id]; ok {
			if now.Sub(timeout) < 30*time.Second {
				continue
			}
			delete(ps.noBlobPeers, id)
		}
		filtered = append(filtered, id)
	}
	return filtered
}

// blobSyncPeers retrieves all of the blob sync peers.
func (ps *peerSet) blobSyncPeers(excludePeer *string) []*beaconPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	peers := make([]*beaconPeer, 0, len(ps.beacons))
	for id, peer := range ps.beacons {
		if peer.BlobSync() {
			if excludePeer != nil && id == *excludePeer {
				continue
			}
			peers = append(peers, peer)
		}
	}
	return peers
}
