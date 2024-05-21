// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "./Errors.sol";

import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";

interface IGovReward {
    function getMiners() external view returns (address[] memory);
}

abstract contract GovernanceVote {
    using EnumerableSet for EnumerableSet.AddressSet;
    struct AddressToBytes32Map {
        EnumerableSet.AddressSet _keys;
        mapping(address key => bytes32) _values;
    }

    // events for voting
    event Vote(address indexed voter, bytes32 indexed methodKey, bytes32 paramKey);
    event VotePass(bytes32 indexed methodKey, bytes32 paramKey);

    // governance reward contact
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // vote mapping, method key -> (user address -> param key)
    mapping(bytes32 => AddressToBytes32Map) private voteMap;

    modifier needVote(bytes32 methodKey, bytes32 paramKey) {
        address[] memory miners = IGovReward(govReward).getMiners();
        if(!_contains(miners, msg.sender) revert Errors.NotMiner();

        // update vote map
        _vote(methodKey, paramKey);

        // check vote, if not pass just return
        uint mlength = miners.length;
        uint validVotes = 0;
        for (uint i = 0; i < mlength; i++) {
            if (voteMap[methodKey]._values[miners[i]] == paramKey) {
                validVotes++;
            }
        }
        if (validVotes < (miners.length + 1) / 2) return;

        // clear vote
        emit VotePass(methodKey, paramKey);
        _clearVote(methodKey);

        // execute method
        _;
    }

    function _vote(bytes32 methodKey, bytes32 paramKey) internal {
        voteMap[methodKey]._values[msg.sender] = paramKey;
        voteMap[methodKey]._keys.add(msg.sender);
        emit Vote(msg.sender, methodKey, paramKey);
    }

    function _clearVote(bytes32 methodKey) internal {
        address[] memory voters = voteMap[methodKey]._keys.values();
        uint vlength = voters.length;
        delete voteMap[methodKey]._keys._inner._values;
        for (uint i = 0; i < vlength; i++) {
            delete voteMap[methodKey]._keys._inner._positions[
                bytes32(uint256(uint160(voters[i])))
            ];
            delete voteMap[methodKey]._values[voters[i]];
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
