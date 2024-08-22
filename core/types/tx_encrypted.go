// Copyright 2021 The go-ethereum Authors
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

package types

// EncryptedTx represents an Encrypted neox transaction. Identical to DynamicFeeTx except for the txType() method
type EncryptedTx struct {
	DynamicFeeTx
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *EncryptedTx) copy() TxData {
	dcp, _ := tx.DynamicFeeTx.copy().(*DynamicFeeTx)
	return &EncryptedTx{DynamicFeeTx: *dcp}
}

// accessors for innerTx.
func (tx *EncryptedTx) txType() byte { return EncryptedTxType }
