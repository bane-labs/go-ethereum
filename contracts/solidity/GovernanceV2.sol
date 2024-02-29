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
    function getRewardAmount(address voter, address candidate) external view returns (uint);
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

    mapping(address => mapping(uint => mapping(address => uint))) regularVotes;
    mapping(address => mapping(address => uint[])) votedRounds;

    function getCurrentRound() public view returns (uint) {
        return getRound(block.timestamp);
    }

    function getRound(uint timestamp) public pure returns (uint) {
        return timestamp / 1209600;
    }

    function vote(address to) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        uint currentRound = getCurrentRound();

        uint voted = regularVotes[msg.sender][currentRound][to];
        // add this round to
        if (voted == 0) {
            votedRounds[msg.sender][to].push(currentRound);
        }
        regularVotes[msg.sender][currentRound][to] = voted + msg.value;

        emit Vote(msg.sender, to, msg.value);
    }

    function revokeVote(address from) external {
        uint currentRound = getCurrentRound();
        uint amount = regularVotes[msg.sender][currentRound][from];
        delete regularVotes[msg.sender][currentRound][from];
        safeTransferETH(msg.sender, amount);
    }

    function withdraw(address from) external {
        uint currentRound = getCurrentRound();
        uint totalAmount = 0;
        uint[] memory votedIndex = votedRounds[msg.sender][from];
        uint indexLength = votedIndex.length;
        for (uint i = 0; i < indexLength; i++) {
            uint round = votedIndex[i];
            // only rounds before the current running one (the one before current voting)
            if (round < currentRound - 1) {
                uint roundAmount = regularVotes[msg.sender][round][from];
                delete regularVotes[msg.sender][round][from];
                delete votedRounds[msg.sender][from][i];
                totalAmount += roundAmount + getRoundReward(round, roundAmount);
            }
        }
        safeTransferETH(msg.sender, totalAmount);
    }

    function getRewardAmount(address voter, address candidate) external view returns (uint) {
        uint currentRound = getCurrentRound();
        uint totalReward = 0;
        uint[] memory votedIndex = votedRounds[voter][candidate];
        uint indexLength = votedIndex.length;
        for (uint i = 0; i < indexLength; i++) {
            uint round = votedIndex[i];
            // only rounds before the current running one (the one before current voting)
            if (round < currentRound - 1) {
                uint roundAmount = regularVotes[voter][round][candidate];
                totalReward += getRoundReward(round, roundAmount);
            }
        }
        return totalReward;
    }

    function getRoundReward(uint round, uint amount) public view returns (uint) {
        return 0;
    }

    function safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }
}