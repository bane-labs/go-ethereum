// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "../constants/Errors.sol";
import "../interfaces/IGovReward.sol";

abstract contract GovernanceVote {
    // events for voting
    event Vote(address indexed voter, bytes32 indexed methodKey, bytes32 paramKey);
    event VotePass(bytes32 indexed methodKey, bytes32 paramKey);

    // governance reward contact
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // vote mapping, method key -> (user address -> param key)
    mapping(bytes32 => mapping(address => bytes32)) private voteMap;

    modifier needVote(bytes32 methodKey, bytes32 paramKey) {
        address[] memory miners = IGovReward(govReward).getMiners();
        if (!_contains(miners, msg.sender)) revert Errors.NotMiner();

        // update vote map
        _vote(methodKey, paramKey);

        // check vote, if not pass just return
        uint length = miners.length;
        uint validVotes = 0;
        for (uint i = 0; i < length; i++) {
            if (voteMap[methodKey][miners[i]] == paramKey) {
                validVotes++;
            }
        }
        if (validVotes < (length + 1) / 2) return;

        // clear vote
        emit VotePass(methodKey, paramKey);
        _clearVote(methodKey);

        // execute method
        _;
    }

    function _vote(bytes32 methodKey, bytes32 paramKey) internal {
        voteMap[methodKey][msg.sender] = paramKey;
        emit Vote(msg.sender, methodKey, paramKey);
    }

    function _clearVote(bytes32 methodKey) internal {
        address[] memory voters = IGovReward(govReward).getMiners();
        uint length = voters.length;
        for (uint i; i < length; i++) {
            delete voteMap[methodKey][voters[i]];
        }
    }

    function _contains(
        address[] memory list,
        address addr
    ) internal pure returns (bool) {
        uint length = list.length;
        for (uint i = 0; i < length; i++) {
            if (addr == list[i]) {
                return true;
            }
        }
        return false;
    }
}
