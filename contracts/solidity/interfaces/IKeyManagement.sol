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
        bytes calldata pubkey
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

    // check if a successful round of dkg sharing and resharing happens between two heights
    function isRoundNumberIncreased(
        uint epochHeight,
        uint lastEpochHeight
    ) external view returns (bool);

    // get the round number of current ongoing dkg
    function roundNumber() external view returns (uint);

    // get the member index of sharing, start from 1, or 0 if not a member.
    function indexOfSharing(address addr) external view returns (uint);

    // get the member index of resharing, start from 1, or 0 if not a member.
    function indexOfResharing(address addr) external view returns (uint);

    // get the member indexes of resharing that need recover, start from 1
    function indexCurrentNeedRecovering() external view returns (uint[] memory);

    // check if a round of dkg sharing is ready
    function isShareReady() external view returns (bool);

    // get public key of addr
    function messagePubkeys(address addr) external view returns (bytes memory);

    // get share msgs by round and index
    function getShareMsgs(
        uint round,
        uint index
    ) external view returns (bytes[] memory);

    // get share pvss by round and index
    function spvsses(
        uint round,
        uint index
    ) external view returns (bytes memory);

    // get reshare msgs by round and index
    function getReshareMsgs(
        uint round,
        uint index
    ) external view returns (bytes[] memory);

    // get reshare pvss by round and index
    function rpvsses(
        uint round,
        uint index
    ) external view returns (bytes memory);

    // get recover msg by round, indexSend and indexReceive
    function recoverMsgs(
        uint round,
        uint indexSend,
        uint indexReceive
    ) external view returns (bytes memory);

    // get shared public key by round and index
    function sharedPubs(
        uint round,
        uint index
    ) external view returns (bytes memory);

    // get aggregated commitment by round
    function aggregatedCommitments(uint round) external view returns (bytes memory);
}
