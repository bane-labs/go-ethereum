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
	"crypto/ecdsa"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyMulti(t *testing.T) {
	const nKeys = 7
	const bftKeys = 5
	const majKeys = 4

	var (
		keys = make([]*ecdsa.PrivateKey, nKeys)
		pubs = make([][]byte, nKeys)
		sigs = make([][]byte, nKeys)
	)

	for i := range keys {
		k, _ := GenerateKey()
		keys[i] = k
		pubs[i] = FromECDSAPub(&k.PublicKey)
		s, err := Sign(testmsg, k)
		require.NoError(t, err)
		sigs[i] = s
	}

	require.NoError(t, VerifyMultiBFT(testmsg, pubs, sigs[:bftKeys]))
	require.NoError(t, VerifyMultiBFT(testmsg, pubs, sigs[nKeys-bftKeys:]))

	sigs[0], sigs[5] = sigs[5], sigs[0]
	require.NoError(t, VerifyMultiBFT(testmsg, pubs, sigs[:bftKeys]))

	sigs[1], sigs[3] = sigs[3], sigs[1]
	require.NoError(t, VerifyMultiBFT(testmsg, pubs, sigs[:bftKeys]))

	require.NoError(t, VerifyMultiMajority(testmsg, pubs, sigs[:majKeys]))

	require.Error(t, VerifyMultiMajority(testmsg, pubs, sigs[:bftKeys]))
	require.Error(t, VerifyMultiMajority(testmsg, pubs, sigs[:1]))

	require.Error(t, VerifyMultiBFT(testmsg, pubs, sigs[:majKeys]))
	require.Error(t, VerifyMultiBFT(testmsg, pubs, sigs[:1]))

	require.NoError(t, VerifyMulti(testmsg, pubs, sigs[:majKeys]))
	require.NoError(t, VerifyMulti(testmsg, pubs, sigs[:bftKeys]))
	require.NoError(t, VerifyMulti(testmsg, pubs, sigs[:1]))

	require.Error(t, VerifyMulti(testmsg, pubs[:1], sigs[:3]))

	// These were not shuffled above
	require.NoError(t, VerifyMulti(testmsg, pubs[2:3], sigs[2:3]))

	// These were.
	require.Error(t, VerifyMulti(testmsg, pubs[:1], sigs[:1]))
	require.Error(t, VerifyMulti(testmsg, pubs[:3], sigs[:1]))

	// Broken signature.
	sigs[0] = append(sigs[0], 'a', 'b', 'c')
	require.Error(t, VerifyMulti(testmsg, pubs, sigs))

	// Duplicate sig.
	sigs[2] = sigs[1]
	require.Error(t, VerifyMulti(testmsg, pubs, sigs[1:3]))

}
