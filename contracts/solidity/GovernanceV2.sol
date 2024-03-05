// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IGovernanceV2 {
    event Register(address candidate);
    event Exit(address candidate);
    event Vote(address voter, address to, uint amount);
    event RevokeVote(address voter, address from, uint amount);
    event WithdrawReward(address voter, uint reward);

    // register to be a candidate with gas
    function registerCandidate() external payable;

    // exit candidates and wait for withdraw
    function exitCandidate() external;

    // withdraw register fee after 1 epoch
    function claimRegisterFee() external;

    // vote with gas, only 1 target is allowed
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
    // register fee
    uint public constant REGISTER_FEE = 1000 ether;
    // minimum duration of an epoch
    uint public constant EPOCH_DURATION = 1209600;
    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // the counter of epoch index
    uint public epochCount;
    // the timestamp of the last time when voting start
    uint public lastEpochTime;
    // candidate list
    address[] public candidateList;
    // the timestamp when register happen
    mapping(address => uint) public registerTime;
    // the timestamp when exit happen
    mapping(address => uint) public exitTime;
    // the left register fee to exit
    mapping(address => uint) public feeBalance;
    // epoch=>uint
    mapping(uint => uint) public epochReward;
    // voter=>epoch=>candidate=>amount
    mapping(address => mapping(uint => mapping(address => uint))) voterTable;
    // voter=>candidate=>epochs
    mapping(address => mapping(address => uint[])) votedEpochs;
    // candidate=>epoch=>amount
    mapping(address => mapping(uint => uint)) receivedVotes;

    function getCurrentEpoch() public view returns (uint) {
        if (block.timestamp > lastEpochTime + EPOCH_DURATION) {
            return epochCount + 1;
        } else {
            return epochCount;
        }
    }

    function getVotedValueByEpoch(
        address voter,
        uint epoch,
        address candidate
    ) public view returns (uint) {
        return voterTable[voter][epoch][candidate];
    }

    function getReceivedVotedByEpoch(
        address candidate,
        uint epoch
    ) public view returns (uint) {
        return receivedVotes[candidate][epoch];
    }

    function registerCandidate() external payable {
        require(msg.value == REGISTER_FEE, "insufficient amount");
        address[] memory list = candidateList;
        uint length = candidateList.length;
        // check duplication
        for (uint i = 0; i < length; i++) {
            if (list[i] == msg.sender) {
                revert("candidate exists");
            }
        }
        // delete exit record
        delete exitTime[msg.sender];
        // add to candidates
        candidateList.push(msg.sender);
        // record register time and balance
        registerTime[msg.sender] = block.timestamp;
        feeBalance[msg.sender] = msg.value;
        emit Register(msg.sender);
    }

    function exitCandidate() external {
        require(registerTime[msg.sender] > 0, "candidate not exists");
        // delete register time, cannot be voted
        delete registerTime[msg.sender];
        // record exit time, but candidate list not removed, balance still locked
        exitTime[msg.sender] = block.timestamp;
        emit Exit(msg.sender);
    }

    function claimRegisterFee() external {
        // require 2 epochs to exit candidate list, so that the last round of vote can work as expected
        require(
            block.timestamp > exitTime[msg.sender] + 2 * EPOCH_DURATION,
            "claim not allowed"
        );

        // reconstruct candidate list
        address[] memory list = candidateList;
        uint length = candidateList.length;
        delete candidateList;
        for (uint i = 0; i < length; i++) {
            if (list[i] != msg.sender) {
                candidateList.push(list[i]);
            }
        }

        // send back balance
        uint amount = feeBalance[msg.sender];
        delete feeBalance[msg.sender];
        _safeTransferETH(msg.sender, amount);
    }

    function vote(address candidateTo) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(registerTime[candidateTo] > 0, "candidate not allowed");
        // the first person vote in new epoch will pay for update
        uint currentEpoch = _getAndUpdateEpochCount();

        uint voted = voterTable[msg.sender][currentEpoch][candidateTo];
        // add this epoch to personal record if never voted
        if (voted == 0) {
            votedEpochs[msg.sender][candidateTo].push(currentEpoch);
        }
        voterTable[msg.sender][currentEpoch][candidateTo] = voted + msg.value;
        receivedVotes[candidateTo][currentEpoch] += msg.value;

        emit Vote(msg.sender, candidateTo, msg.value);
    }

    function revokeVote(address candidateFrom) external {
        // revoke will not trigger epoch change
        uint currentEpoch = getCurrentEpoch();
        uint amount = voterTable[msg.sender][currentEpoch][candidateFrom];
        receivedVotes[candidateFrom][currentEpoch] -= amount;
        delete voterTable[msg.sender][currentEpoch][candidateFrom];
        _safeTransferETH(msg.sender, amount);

        emit RevokeVote(msg.sender, candidateFrom, amount);
    }

    function withdraw(address candidateFrom) external {
        // withdraw will not trigger epoch change
        uint currentEpoch = getCurrentEpoch();
        uint totalAmount = 0;
        uint totalReward = 0;
        // loop all voted epochs
        uint[] memory votedIndex = votedEpochs[msg.sender][candidateFrom];
        uint indexLength = votedIndex.length;
        delete votedEpochs[msg.sender][candidateFrom];
        for (uint i = 0; i < indexLength; i++) {
            uint epoch = votedIndex[i];
            // only epochs before the current running one (the one before current voting)
            if (epoch < currentEpoch - 1) {
                uint epochAmount = voterTable[msg.sender][epoch][candidateFrom];
                delete voterTable[msg.sender][epoch][candidateFrom];
                totalAmount += epochAmount;
                totalReward += getEpochReward(epoch, epochAmount);
            } else if (epoch >= currentEpoch - 1) {
                // reconstructed array, the new one always shorter than 2
                votedEpochs[msg.sender][candidateFrom].push(epoch);
            }
        }
        _safeTransferETH(msg.sender, totalAmount + totalReward);
        emit WithdrawReward(msg.sender, totalReward);
    }

    function getRewardAmount(
        address voter,
        address candidate
    ) external view returns (uint) {
        uint currentEpoch = getCurrentEpoch();
        uint totalReward = 0;
        uint[] memory votedIndex = votedEpochs[voter][candidate];
        uint indexLength = votedIndex.length;
        for (uint i = 0; i < indexLength; i++) {
            uint epoch = votedIndex[i];
            // only epochs before the current running one (the one before current voting)
            if (epoch < currentEpoch - 1) {
                uint epochAmount = voterTable[voter][epoch][candidate];
                totalReward += getEpochReward(epoch, epochAmount);
            } else {
                break;
            }
        }
        return totalReward;
    }

    function getEpochReward(uint epoch, uint share) public view returns (uint) {
        return 0;
    }

    function getCurrentConsensus() public view returns (address[7] memory) {
        // build up a votes array
        uint length = candidateList.length;
        uint epoch = epochCount - 1;
        uint[] memory votes;
        for (uint i = 0; i < length; i++) {
            _push(votes, receivedVotes[candidateList[i]][epoch]);
        }
        address[] memory candidates = candidateList;

        // sort based on votes
        _quickSort(candidates, votes, 0, int(length - 1));

        // return the first 7 candidates as consensus list
        address[7] memory consensus;
        for (uint i = 0; i < 7; i++) {
            consensus[i] = candidates[i];
        }
        return consensus;
    }

    function _getAndUpdateEpochCount() internal returns (uint) {
        if (block.timestamp > lastEpochTime + EPOCH_DURATION) {
            epochCount += 1;
            lastEpochTime = block.timestamp;
        }
        return epochCount;
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }

    // sort candidates from high votes to low
    function _quickSort(
        address[] memory candidates,
        uint[] memory votes,
        int left,
        int right
    ) internal pure {
        int i = left;
        int j = right;
        if (i == j) return;
        uint pivot = votes[uint(left + (right - left) / 2)];
        while (i <= j) {
            while (votes[uint(i)] < pivot) i++;
            while (pivot < votes[uint(j)]) j--;
            if (i <= j) {
                (votes[uint(i)], votes[uint(j)]) = (
                    votes[uint(j)],
                    votes[uint(i)]
                );
                (candidates[uint(i)], candidates[uint(j)]) = (
                    candidates[uint(j)],
                    candidates[uint(i)]
                );
                i++;
                j--;
            }
        }
        if (left < j) _quickSort(candidates, votes, left, j);
        if (i < right) _quickSort(candidates, votes, i, right);
    }

    // push an element to the end of a memory array, note the array can not be changed if another dynamic array is declared after this
    function _push(uint[] memory _nums, uint _num) internal pure {
        assembly {
            mstore(add(_nums, mul(add(mload(_nums), 1), 0x20)), _num)
            mstore(_nums, add(mload(_nums), 1))
            mstore(0x40, add(mload(0x40), 0x20))
        }
    }
}
