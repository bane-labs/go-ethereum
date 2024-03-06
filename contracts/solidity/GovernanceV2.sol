// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IGovernanceV2 {
    event Register(address candidate);
    event Exit(address candidate);
    event Vote(address voter, address to, uint amount);
    event RevokeVote(address voter, address from, uint amount);
    event VoterWithdraw(address voter, uint votes, uint reward);
    event CandidateClaim(address candidate, uint reward);

    // register to be a candidate with gas
    function registerCandidate(uint shareRate) external payable;

    // exit candidates and wait for withdraw
    function exitCandidate() external;

    // withdraw register fee after 1 epoch
    function claimRegisterFee() external;

    // vote with gas, only 1 target is allowed
    function vote(address to) external payable;

    // revoke ongoing vote
    function revokeVote() external;

    // withdraw past vote
    function voterWithdraw() external;

    // claim rewards for being a consensus member
    function candidateClaim() external;
}

interface IGovReward {
    function withdraw() external;
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

    // counter of epoch index
    uint public epochCount;
    // timestamp of the last time when voting starts
    uint public lastEpochTime;
    // candidate list
    address[] public candidateList;
    // settings about how much reward given to voter
    mapping(address => uint) public shareRateOf;
    // timestamp when register happens
    mapping(address => uint) public registerTimeOf;
    // the timestamp when exit happens
    mapping(address => uint) public exitTimeOf;
    // the left register fee to exit
    mapping(address => uint) public candidateBalanceOf;
    // epoch=>uint
    mapping(uint => uint) public epochRewards;
    // voter=>epoch=>candidate
    mapping(address => mapping(uint => address)) public votedTo;
    // voter=>epoch=>amount
    mapping(address => mapping(uint => uint)) public votedAmount;
    // voter=>epochs
    mapping(address => uint[]) public unclaimedEpochsOf;
    // candidate=>epoch=>amount
    mapping(address => mapping(uint => uint)) public receivedVotes;
    // candidate=>epoch
    mapping(address => uint) public lastClaimedEpochOf;
    // epoch=>consensus
    mapping(uint => address[7]) private consensusCache;

    constructor() {
        address[7] memory initialConsensus = [
            address(0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266),
            address(0x70997970C51812dc3A010C7d01b50e0d17dc79C8),
            address(0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC),
            address(0x90F79bf6EB2c4f870365E785982E1f101E93b906),
            address(0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65),
            address(0x9965507D1a55bcC2695C58ba16FB37d819B0A4dc),
            address(0x976EA74026E726554dB657fA54763abd0C3a0aa9)
        ];
        consensusCache[0] = initialConsensus;
    }

    receive() external payable {
        uint epoch = getRealCurrentEpoch();
        if (epoch > 0) {
            epochRewards[epoch] += msg.value;
        } else {
            epochRewards[epoch + 1] += msg.value;
        }
    }

    function getNominalCurrentEpoch() public view returns (uint) {
        if (block.timestamp > lastEpochTime + EPOCH_DURATION) {
            return epochCount + 1;
        } else {
            return epochCount;
        }
    }

    function getRealCurrentEpoch() public view returns (uint) {
        return epochCount;
    }

    function _getAndUpdateEpochCount() internal returns (uint) {
        if (block.timestamp > lastEpochTime + EPOCH_DURATION) {
            IGovReward(govReward).withdraw();
            epochCount += 1;
            lastEpochTime = block.timestamp;
        }
        return epochCount;
    }

    function getVotedByEpoch(
        address voter,
        uint epoch
    ) public view returns (address, uint) {
        return (votedTo[voter][epoch], votedAmount[voter][epoch]);
    }

    function getReceivedVotesByEpoch(
        address candidate,
        uint epoch
    ) public view returns (uint) {
        return receivedVotes[candidate][epoch];
    }

    function registerCandidate(uint shareRate) external payable {
        require(msg.value == REGISTER_FEE, "insufficient amount");
        require(shareRate < 1000, "invalid rate");
        address[] memory list = candidateList;
        uint length = candidateList.length;
        // check duplication
        bool existed = false;
        for (uint i = 0; i < length; i++) {
            if (list[i] == msg.sender) {
                existed = true;
                break;
            }
        }
        if (!existed) {
            // add to candidates
            candidateList.push(msg.sender);
        }
        // record register time, share rate and balance
        registerTimeOf[msg.sender] = block.timestamp;
        shareRateOf[msg.sender] = shareRate;
        candidateBalanceOf[msg.sender] = msg.value;
        // set the start point for claim
        lastClaimedEpochOf[msg.sender] = getRealCurrentEpoch();
        emit Register(msg.sender);
    }

    function exitCandidate() external {
        require(registerTimeOf[msg.sender] > 0, "candidate not exists");
        // delete register time, cannot be voted
        delete registerTimeOf[msg.sender];
        // record exit time, but candidate list not removed, balance still locked
        exitTimeOf[msg.sender] = block.timestamp;
        emit Exit(msg.sender);
    }

    function claimRegisterFee() external {
        // require 2 epochs to exit candidate list, so that the last round of vote can work as expected
        require(
            block.timestamp > exitTimeOf[msg.sender] + 2 * EPOCH_DURATION,
            "claim not allowed"
        );

        // send back balance
        uint amount = candidateBalanceOf[msg.sender];
        delete candidateBalanceOf[msg.sender];
        _safeTransferETH(msg.sender, amount);
    }

    function vote(address candidateTo) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(registerTimeOf[candidateTo] > 0, "candidate not allowed");
        // the first person vote in new epoch will pay for update
        uint currentEpoch = _getAndUpdateEpochCount();

        uint voted = votedAmount[msg.sender][currentEpoch];
        // add this epoch to personal record if never voted
        if (voted == 0) {
            votedTo[msg.sender][currentEpoch] = candidateTo;
            unclaimedEpochsOf[msg.sender].push(currentEpoch);
        } else {
            require(
                votedTo[msg.sender][currentEpoch] == candidateTo,
                "only one choice is allowed"
            );
        }
        votedAmount[msg.sender][currentEpoch] = voted + msg.value;
        receivedVotes[candidateTo][currentEpoch] += msg.value;

        emit Vote(msg.sender, candidateTo, msg.value);
    }

    function revokeVote() external {
        // revoke will not trigger epoch change
        uint currentEpoch = getNominalCurrentEpoch();
        address candidateFrom = votedTo[msg.sender][currentEpoch];
        uint amount = votedAmount[msg.sender][currentEpoch];
        receivedVotes[candidateFrom][currentEpoch] -= amount;
        delete votedTo[msg.sender][currentEpoch];
        delete votedAmount[msg.sender][currentEpoch];
        _safeTransferETH(msg.sender, amount);

        emit RevokeVote(msg.sender, candidateFrom, amount);
    }

    function voterWithdraw() external {
        // use epochCount, to lock votes and rewards until epoch change
        uint currentEpoch = getRealCurrentEpoch();
        uint totalAmount = 0;
        uint totalReward = 0;
        // loop all voted epochs
        uint[] memory votedIndex = unclaimedEpochsOf[msg.sender];
        uint indexLength = votedIndex.length;
        delete unclaimedEpochsOf[msg.sender];
        for (uint i = 0; i < indexLength; i++) {
            uint epoch = votedIndex[i];
            // only epochs before the current running one (the one before current voting)
            if (epoch < currentEpoch - 1) {
                uint epochAmount = votedAmount[msg.sender][epoch];
                delete votedAmount[msg.sender][epoch];
                totalAmount += epochAmount;

                // calculate reward
                address candidate = votedTo[msg.sender][epoch];
                address[7] memory consensus = _tryGetAndCacheConsensus(epoch);
                bool included = false;
                for (uint j = 0; j < 7; j++) {
                    if (consensus[j] == candidate) {
                        included = true;
                    }
                }
                if (included) {
                    totalReward +=
                        (epochAmount *
                            epochRewards[epoch + 1] *
                            shareRateOf[candidate]) /
                        receivedVotes[candidate][epoch] /
                        7 /
                        1000;
                }
            } else if (epoch >= currentEpoch - 1) {
                // reconstructed array, the new one always shorter than 2
                unclaimedEpochsOf[msg.sender].push(epoch);
            }
        }
        _safeTransferETH(msg.sender, totalAmount + totalReward);
        emit VoterWithdraw(msg.sender, totalAmount, totalReward);
    }

    function candidateClaim() external {
        require(
            registerTimeOf[msg.sender] > 0 || exitTimeOf[msg.sender] > 0,
            "not a candidate"
        );
        // use epochCount, to lock rewards until epoch change
        uint currentEpoch = getRealCurrentEpoch();
        require(currentEpoch > 2, "claim not started");
        uint totalReward = 0;
        // loop all unclaimed epochs
        for (
            uint i = lastClaimedEpochOf[msg.sender] + 1;
            i < currentEpoch - 1;
            i++
        ) {
            // only epochs before the current running one (the one before current voting)
            address[7] memory consensus = _tryGetAndCacheConsensus(i);
            bool included = false;
            for (uint j = 0; j < 7; j++) {
                if (consensus[j] == msg.sender) {
                    included = true;
                }
            }
            if (included) {
                totalReward +=
                    (epochRewards[i + 1] * (1000 - shareRateOf[msg.sender])) /
                    7 /
                    1000;
            }
        }
        lastClaimedEpochOf[msg.sender] = currentEpoch - 2;
        _safeTransferETH(msg.sender, totalReward);
        emit CandidateClaim(msg.sender, totalReward);
    }

    function getCurrentConsensus() public view returns (address[7] memory) {
        uint epoch = getRealCurrentEpoch();
        if (epoch > 0) {
            return getConsensus(epoch - 1);
        } else {
            return getConsensus(epoch);
        }
    }

    function getConsensus(uint epoch) public view returns (address[7] memory) {
        address[7] memory cache = consensusCache[epoch];
        if (cache[0] == address(0)) {
            return _getConsensus(epoch);
        }
        return cache;
    }

    function _tryGetAndCacheConsensus(
        uint epoch
    ) internal returns (address[7] memory) {
        address[7] memory cache = consensusCache[epoch];
        if (cache[0] == address(0)) {
            cache = _getConsensus(epoch);
            consensusCache[epoch] = cache;
        }
        return cache;
    }

    function _getConsensus(
        uint epoch
    ) internal view returns (address[7] memory) {
        // build up a votes array
        address[] memory candidates = candidateList;
        uint length = candidateList.length;
        uint[] memory votes = new uint[](length);
        for (uint i = 0; i < length; i++) {
            votes[i] = receivedVotes[candidateList[i]][epoch];
        }

        // sort based on votes
        _quickSort(candidates, votes, 0, int(length - 1));

        // return the first 7 candidates as consensus list
        address[7] memory consensus;
        for (uint i = 0; i < 7; i++) {
            consensus[i] = candidates[i];
        }
        return consensus;
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
            while (votes[uint(i)] > pivot) i++;
            while (pivot > votes[uint(j)]) j--;
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
}
