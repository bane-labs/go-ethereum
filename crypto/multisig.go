// Copyright 2023 NeoSPCC
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

package crypto

import (
	"bytes"
	"errors"
	"sort"

	"github.com/ethereum/go-ethereum/common"
)

// GetBFTHonestNodeCount returns minimum number of honest nodes
// required for BFT network of size n.
func GetBFTHonestNodeCount(n int) int {
	return n - (n-1)/3
}

// GetMajorityHonestNodeCount returns minimum number of honest nodes
// required for majority-style agreement.
func GetMajorityHonestNodeCount(n int) int {
	return n - (n-1)/2
}

// VerifyMultiBFT checks for BFT (> 2/3) number of sigs made by pubs for hash.
func VerifyMultiBFT(hash []byte, pubs []common.Address, sigs [][]byte) error {
	var (
		n = len(pubs)
		m = GetBFTHonestNodeCount(n)
	)
	if len(sigs) != m {
		return errors.New("wrong number of signatures")
	}
	return VerifyMulti(hash, pubs, sigs)
}

// VerifyMultiMajority checks for majority (> 1/2) number of sigs made by pubs for hash.
func VerifyMultiMajority(hash []byte, pubs []common.Address, sigs [][]byte) error {
	var (
		n = len(pubs)
		m = GetMajorityHonestNodeCount(n)
	)
	if len(sigs) != m {
		return errors.New("wrong number of signatures")
	}
	return VerifyMulti(hash, pubs, sigs)
}

// VerifyMulti verifies that hash was signed by a subset of keys specified
// in pubs. Checking the number of sigs is out of scope, see [VerifyMultiBFT]
// and [VerifyMultiMajority].
func VerifyMulti(hash []byte, pubs []common.Address, sigs [][]byte) error {
	if len(pubs) < len(sigs) {
		return errors.New("number of public keys and signatures doesn't match")
	}

	var (
		vPubs = make([]common.Address, len(pubs))
		sPubs = make([]common.Address, 0, len(pubs))
	)
	copy(vPubs, pubs)
	sort.Slice(vPubs, func(i, j int) bool {
		return bytes.Compare(vPubs[i][:], vPubs[j][:]) < 0
	})
	for i := range sigs {
		pubkey, err := Ecrecover(hash, sigs[i])
		if err != nil {
			return err
		}

		sPubs = append(sPubs, PubkeyBytesToAddress(pubkey))
	}
	sort.Slice(sPubs, func(i, j int) bool {
		return bytes.Compare(sPubs[i][:], sPubs[j][:]) < 0
	})
	var vi int
	for si := range sPubs {
		var match bool
		for vi < len(vPubs) {
			if vPubs[vi] == sPubs[si] {
				match = true
			}
			vi++
			if match {
				break
			}
		}
		if !match {
			return errors.New("invalid signature")
		}
	}
	return nil
}
