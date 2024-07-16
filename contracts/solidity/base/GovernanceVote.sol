// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "../libraries/Errors.sol";
import {IGovReward} from "../interfaces/IGovReward.sol";

abstract contract GovernanceVote {
    // events for voting
    event Vote(
        address indexed voter,
        bytes32 indexed methodKey,
        bytes32 paramKey
    );
    event VotePass(bytes32 indexed methodKey, bytes32 paramKey);

    // governance reward contact
    address public constant GOV_REWARD =
        0x1212000000000000000000000000000000000003;

    struct VoteInfo {
        // vote mapping, user address -> param key
        mapping(address voter => bytes32) values;
        // voter, used for clearing vote after voting is passed
        address[] voters;
    }

    // vote mapping, method key -> voteInfo
    mapping(bytes32 => VoteInfo) private voteMap;

    modifier needVote(bytes32 methodKey, bytes32 paramKey) {
        address[] memory miners = IGovReward(GOV_REWARD).getMiners();
        if (!_contains(miners, msg.sender)) revert Errors.NotMiner();
        // update vote map
        _vote(methodKey, paramKey);
        // check vote, if not pass just return
        if (!_checkVote(methodKey, paramKey, miners)) {
            return;
        }
        emit VotePass(methodKey, paramKey);
        // clear vote
        _clearVote(methodKey);
        // execute method
        _;
    }

    function _vote(bytes32 methodKey, bytes32 paramKey) internal {
        if (voteMap[methodKey].values[msg.sender] == bytes32(0)) {
            voteMap[methodKey].voters.push(msg.sender);
        }
        voteMap[methodKey].values[msg.sender] = paramKey;
        emit Vote(msg.sender, methodKey, paramKey);
    }

    function _clearVote(bytes32 methodKey) internal {
        address[] memory voters = voteMap[methodKey].voters;
        for (uint i; i < voters.length; i++) {
            delete voteMap[methodKey].values[voters[i]];
        }
        // this will not clear the mapping in VoteInfo, so we need the above code
        delete voteMap[methodKey];
    }

    function _checkVote(
        bytes32 methodKey,
        bytes32 paramKey,
        address[] memory voters
    ) internal view returns (bool isPass) {
        uint votedCount;
        for (uint i; i < voters.length; i++) {
            if (voteMap[methodKey].values[voters[i]] == paramKey) {
                votedCount++;
            }
        }
        return votedCount > voters.length / 2;
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
