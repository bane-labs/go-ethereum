// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {OneMessageVerifier} from "../libraries/OneMessageVerifier.sol";
import {TwoMessageVerifier} from "../libraries/TwoMessageVerifier.sol";
import {SevenMessageVerifier} from "../libraries/SevenMessageVerifier.sol";
import {KeyManagement} from "../KeyManagement.sol";

contract MockDKGVerifier is KeyManagement {
    function mockMessageKey(address addr, bytes calldata pubkey) external {
        messagePubkeys[addr] = pubkey;
    }

    function mockPVSS(uint[] calldata idxs, bytes[] calldata pvsses) external {
        uint length = idxs.length;
        require(pvsses.length == length);
        for (uint i = 0; i < length; i++) {
            spvsses[0][idxs[i]] = pvsses[i];
        }
    }

    function verifyShareProof(
        bytes calldata pvss,
        bytes[] calldata messages,
        address[] calldata receivers,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) public view {
        require(messages.length == receivers.length);
        bytes32 pubHash = _computeVerifierHashInputForShareOrReshare(
            7,
            5,
            pvss,
            messages,
            receivers
        );
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        SevenMessageVerifier.verifyProof(
            proof,
            commitments,
            commitmentPok,
            input
        );
    }

    function verifyRecoverProof(
        uint[] calldata idxs,
        bytes[] calldata messages,
        address[] calldata participants,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) public view {
        uint length = idxs.length;
        require(messages.length == length);
        // use mocked state for verification
        bytes32 pubHash = _computeVerifierHashInputForRecover(
            1,
            5,
            1,
            idxs,
            messages,
            participants
        );
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        if (length == 1) {
            OneMessageVerifier.verifyProof(
                proof,
                commitments,
                commitmentPok,
                input
            );
        } else {
            TwoMessageVerifier.verifyProof(
                proof,
                commitments,
                commitmentPok,
                input
            );
        }
    }
}
