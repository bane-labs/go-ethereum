// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IKeyManagement {
    event Share(
        uint indexed targetEpochHeight,
        uint indexed index,
        address indexed sender
    );
    event Reshare(
        uint indexed targetEpochHeight,
        uint indexed index,
        address indexed sender
    );
    event Recover(
        uint indexed targetEpochHeight,
        uint indexed index,
        address indexed sender
    );

    // register or update the Secp256k1 pubkey for sharing message encryption
    function registerMessageKey(
        address candidate,
        string calldata pubkey
    ) external;

    // upload reshare messages and related commitment, only allowed in the
    // first period, participants must be a member of the current consensus.
    function reshare(bytes calldata pvss, bytes[] calldata messages) external;

    // upload share messages and related commitment, only allowed in the
    // forth period, participants must be a member of the pending consensus.
    function share(bytes calldata pvss, bytes[] calldata messages) external;

    // upload recover messages, only allowed in the second period, participants
    // must be a member of the current consensus.
    function recover(uint[] calldata idxs, bytes[] calldata messages) external;

    // upload reshare messages and related commitment, only allowed in the
    // first period, participants must be a member of the pending consensus.
    function reshareRecovered(
        bytes calldata pvss,
        bytes[] calldata messages
    ) external;

    // compute and update cached global keys
    function onPersistV2() external;

    // check if a round of dkg sharing and resharing are ready
    function isCurrentRoundReady() external view returns (bool);

    // get the member index of sharing, start from 1, or 0 if not a member.
    function indexOfSharing(address addr) external view returns (uint);

    // get the member index of resharing, start from 1, or 0 if not a member.
    function indexOfResharing(address addr) external view returns (uint);

    // get the member indexes of resharing that need recover, start from 1 
    function indexCurrentNeedRecovering() external view returns (uint[] memory);
}
