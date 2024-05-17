// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IGovReward {
    function getMiners() external view returns (address[] memory);
}

abstract contract GovernanceVote {
    // events for voting
    event Vote(address indexed voter, bytes32 indexed methodKey, bytes32 paramKey);
    event VotePass(bytes32 indexed methodKey, bytes32 paramKey);

    // governance reward contact
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // vote mapping, method key ->(user address -> param key)
    mapping(bytes32 => mapping(address => bytes32)) private voteMap;

    function isMiner(address addr) public view returns (bool) {
        address[] memory miners = IGovReward(govReward).getMiners();
        for (uint i = 0; i < miners.length; i++) {
            if (addr == miners[i]) {
                return true;
            }
        }
        return false;
    }

    function vote(bytes32 methodKey, bytes32 paramKey) internal {
        voteMap[methodKey][msg.sender] = paramKey;
        emit Vote(msg.sender, methodKey, paramKey);
    }

    function clearVote(bytes32 methodKey) internal {
        address[] memory voters = IGovReward(govReward).getMiners();
        for (uint i; i < voters.length; i++) {
            delete voteMap[methodKey][voters[i]];
        }
    }

    function checkVote(
        bytes32 methodKey,
        bytes32 paramKey
    ) internal view returns (bool isPass) {
        address[] memory voters = IGovReward(govReward).getMiners();
        uint votedCount;
        for (uint i; i < voters.length; i++) {
            if (voteMap[methodKey][voters[i]] == paramKey) {
                votedCount++;
            }
        }
        return votedCount >= (voters.length + 1) / 2;
    }

    modifier needVote(bytes32 methodKey, bytes32 paramKey) {
        require(isMiner(msg.sender), "not Miner");
        // update vote map
        vote(methodKey, paramKey);
        // check vote, if not pass just return
        if (!checkVote(methodKey, paramKey)) {
            return;
        }
        // execute method
        _;
        emit VotePass(methodKey, paramKey);
        // clear vote
        clearVote(methodKey);
    }
}
