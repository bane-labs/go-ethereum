// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IZKDKGV1 {
    // upload reshare messages and related commitment, only allowed in the
    // first period, participants must be a member of the current consensus.
    function reshare(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external;

    // upload share messages and related commitment, only allowed in the
    // forth period, participants must be a member of the pending consensus.
    function share(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external;

    // upload recover messages, only allowed in the second period, participants
    // must be a member of the current consensus.
    function recover(
        uint[] calldata idxs,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external;

    // upload reshare messages and related commitment, only allowed in the
    // first period, participants must be a member of the pending consensus.
    function reshareRecovered(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external;
}
