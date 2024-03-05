// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IGovernanceV2 {
    event Vote(address voter, address to, uint amount);
    event RevokeVote(address voter, address from, uint amount);
    event WithdrawReward(address voter, uint reward);

    // vote draft with gas, only 1 target is allowed
    function vote(address to) external payable;

    // withdraw ongoing vote
    function revokeVote(address from) external;

    // withdraw past vote
    function withdraw(address from) external;

    // get reward amount of addr
    function getRewardAmount(
        address voter,
        address candidate
    ) external view returns (uint);
}

interface IGovReward {
    function withdrawERC20(address to, address token, uint amount) external;

    function withdraw(address to, uint amount) external;
}

contract GovernanceV2 is IGovernanceV2 {
    // the min balance for voting
    uint public constant MIN_VOTE_AMOUNT = 1 ether;
    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // the counter of round index
    uint public roundCount;
    // the timestamp of the last time when voting start
    uint public lastRoundTime;
    // voter=>round=>candidate=>amount
    mapping(address => mapping(uint => mapping(address => uint))) voterTable;
    // voter=>candidate=>rounds
    mapping(address => mapping(address => uint[])) votedRounds;
    // candidate=>round=>amount
    mapping(address => mapping(uint => uint)) receivedVotes;

    function getCurrentRound() public view returns (uint) {
        if (block.timestamp > lastRoundTime + 1209600) {
            return roundCount + 1;
        } else {
            return roundCount;
        }
    }

    function getVotedValueByRound(
        address voter,
        uint round,
        address candidate
    ) public view returns (uint) {
        return voterTable[voter][round][candidate];
    }

    function getReceivedVotedByRound(
        address candidate,
        uint round
    ) public view returns (uint) {
        return receivedVotes[candidate][round];
    }

    function vote(address candidateTo) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        uint currentRound = _getAndUpdateRoundCount();

        uint voted = voterTable[msg.sender][currentRound][candidateTo];
        // add this round to
        if (voted == 0) {
            votedRounds[msg.sender][candidateTo].push(currentRound);
        }
        voterTable[msg.sender][currentRound][candidateTo] = voted + msg.value;
        receivedVotes[candidateTo][currentRound] += msg.value;

        emit Vote(msg.sender, candidateTo, msg.value);
    }

    function revokeVote(address candidateFrom) external {
        uint currentRound = getCurrentRound();
        uint amount = voterTable[msg.sender][currentRound][candidateFrom];
        receivedVotes[candidateFrom][currentRound] -= amount;
        delete voterTable[msg.sender][currentRound][candidateFrom];
        _safeTransferETH(msg.sender, amount);

        emit RevokeVote(msg.sender, candidateFrom, amount);
    }

    function withdraw(address candidateFrom) external {
        uint currentRound = getCurrentRound();
        uint totalAmount = 0;
        uint totalReward = 0;
        uint[] memory votedIndex = votedRounds[msg.sender][candidateFrom];
        uint indexLength = votedIndex.length;
        delete votedRounds[msg.sender][candidateFrom];
        for (uint i = 0; i < indexLength; i++) {
            uint round = votedIndex[i];
            // only rounds before the current running one (the one before current voting)
            if (round < currentRound - 1) {
                uint roundAmount = voterTable[msg.sender][round][candidateFrom];
                delete voterTable[msg.sender][round][candidateFrom];
                totalAmount += roundAmount;
                totalReward += getRoundReward(round, roundAmount);
            } else if (round >= currentRound - 1) {
                // reconstructed array, the new one always shorter than 2
                votedRounds[msg.sender][candidateFrom].push(round);
            }
        }
        _safeTransferETH(msg.sender, totalAmount + totalReward);
        emit WithdrawReward(msg.sender, totalReward);
    }

    function getRewardAmount(
        address voter,
        address candidate
    ) external view returns (uint) {
        uint currentRound = getCurrentRound();
        uint totalReward = 0;
        uint[] memory votedIndex = votedRounds[voter][candidate];
        uint indexLength = votedIndex.length;
        for (uint i = 0; i < indexLength; i++) {
            uint round = votedIndex[i];
            // only rounds before the current running one (the one before current voting)
            if (round < currentRound - 1) {
                uint roundAmount = voterTable[voter][round][candidate];
                totalReward += getRoundReward(round, roundAmount);
            } else {
                break;
            }
        }
        return totalReward;
    }

    function getRoundReward(uint round, uint share) public view returns (uint) {
        return 0;
    }

    function _getAndUpdateRoundCount() internal returns (uint) {
        if (block.timestamp > lastRoundTime + 1209600) {
            roundCount += 1;
            lastRoundTime = block.timestamp;
        }
        return roundCount;
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }
}
