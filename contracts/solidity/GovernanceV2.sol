// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";

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

    // get the current selected consensus group
    function getCurrentConsensus() external returns (address[] memory);
}

interface IGovReward {
    function withdraw() external;
}

contract GovernanceV2 is IGovernanceV2 {
    using EnumerableSet for EnumerableSet.AddressSet;

    uint public constant CONSENSUS_SIZE = 7;
    // the min balance for voting
    uint public constant MIN_VOTE_AMOUNT = 1 ether;
    // register fee
    uint public constant REGISTER_FEE = 1000 ether;
    // the min vote amount to change epoch
    uint public constant MIN_TOTAL_VOTE = 3000000 ether;
    // minimum duration of an epoch (in blocks)
    uint public constant EPOCH_DURATION = 120960;
    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // counter of epoch index
    uint public epochCount;
    // the last block height when voting starts
    uint public lastEpochHeight;
    // candidate list
    EnumerableSet.AddressSet internal candidateList;
    // epoch=>uint
    mapping(uint => uint) public epochRewards;
    // epoch=>amount
    mapping(uint => uint) public totalVotes;
    // epoch=>amount
    mapping(uint => uint) public votedCandidates;
    // epoch=>consensus
    mapping(uint => address[]) private consensusCache;
    // settings about how much reward given to voter
    mapping(address => uint) public shareRateOf;
    // the timestamp when register happens
    mapping(address => uint) public registerEpochOf;
    // the epoch when exit happens
    mapping(address => uint) public exitEpochOf;
    // the left register fee to exit
    mapping(address => uint) public candidateBalanceOf;
    // candidate=>epoch
    mapping(address => uint) public claimStartEpochOf;
    // candidate=>epoch=>amount
    mapping(address => mapping(uint => uint)) public receivedVotes;
    // voter=>epoch=>candidate
    mapping(address => mapping(uint => address)) public votedTo;
    // voter=>epoch=>amount
    mapping(address => mapping(uint => uint)) public votedAmount;
    // voter=>epochs
    mapping(address => uint[]) public unclaimedEpochsOf;

    constructor() {
        address[7] memory initialConsensus = [
            address(0x74f4EFFb0B538BAec703346b03B6d9292f53A4CD),
            address(0x910AD1641B7125Eff746acCdCa1F11148b22f472),
            address(0xfEf5F250aF14DF73f983cAAb7b1F5002189c42E0),
            address(0xc51964013acbC6b271FEeCB0feBD9E7A01202930),
            address(0xC5bbD9652546BC96bE3DEc97a38eE335f7873Dfa),
            address(0x26F1794B81dF2B832545b8B6bbcA196b82E4fEB1),
            address(0x0B51369D02e47EE3f143391B837Aa08c31AAA19b)
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
        if (block.number > lastEpochHeight + EPOCH_DURATION) {
            return epochCount + 1;
        } else {
            return epochCount;
        }
    }

    function getRealCurrentEpoch() public view returns (uint) {
        return epochCount;
    }

    function _getAndUpdateEpochCount() internal returns (uint) {
        if (
            (block.number > lastEpochHeight + EPOCH_DURATION &&
                totalVotes[epochCount] >= MIN_TOTAL_VOTE &&
                votedCandidates[epochCount] >= CONSENSUS_SIZE) ||
            epochCount == 0
        ) {
            IGovReward(govReward).withdraw();
            epochCount += 1;
            lastEpochHeight = block.number;
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

    function getCandidates() public view returns (address[] memory) {
        return candidateList.values();
    }

    function registerCandidate(uint shareRate) external payable {
        require(msg.value == REGISTER_FEE, "insufficient amount");
        require(shareRate < 1000, "invalid rate");
        require(!candidateList.contains(msg.sender), "candidate exists");
        candidateList.add(msg.sender);

        uint epoch = _getAndUpdateEpochCount();
        // record register time, share rate and balance
        registerEpochOf[msg.sender] = epoch;
        shareRateOf[msg.sender] = shareRate;
        candidateBalanceOf[msg.sender] = msg.value;
        // set the start point for claim
        claimStartEpochOf[msg.sender] = epoch;
        emit Register(msg.sender);
    }

    function exitCandidate() external {
        require(registerEpochOf[msg.sender] > 0, "candidate not exists");
        // delete register time, cannot be voted
        delete registerEpochOf[msg.sender];
        // record exit time, but candidate list not removed, balance still locked
        exitEpochOf[msg.sender] = _getAndUpdateEpochCount();
        emit Exit(msg.sender);
    }

    function claimRegisterFee() external {
        // require 2 epochs to exit candidate list, so that the last round of vote can work as expected
        uint epoch = _getAndUpdateEpochCount();
        require(epoch > exitEpochOf[msg.sender] + 1, "claim not allowed");

        // make sure all consensus are settled
        for (uint i = claimStartEpochOf[msg.sender]; i < epoch - 1; i++) {
            _tryGetAndCacheConsensus(i);
        }

        // reorg candidate list
        candidateList.remove(msg.sender);

        // send back balance
        uint amount = candidateBalanceOf[msg.sender];
        delete candidateBalanceOf[msg.sender];
        _safeTransferETH(msg.sender, amount);
    }

    function vote(address candidateTo) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(registerEpochOf[candidateTo] > 0, "candidate not allowed");
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

        uint received = receivedVotes[candidateTo][currentEpoch];
        if (received == 0) {
            votedCandidates[currentEpoch] += 1;
        }
        receivedVotes[candidateTo][currentEpoch] = received + msg.value;
        totalVotes[currentEpoch] += msg.value;

        emit Vote(msg.sender, candidateTo, msg.value);
    }

    function revokeVote() external {
        // revoke will not trigger epoch change
        uint currentEpoch = getNominalCurrentEpoch();
        address candidateFrom = votedTo[msg.sender][currentEpoch];
        uint amount = votedAmount[msg.sender][currentEpoch];

        uint received = receivedVotes[candidateFrom][currentEpoch];
        if (received == amount) {
            votedCandidates[currentEpoch] -= 1;
        }
        receivedVotes[candidateFrom][currentEpoch] = received - amount;
        totalVotes[currentEpoch] -= amount;
        delete votedTo[msg.sender][currentEpoch];
        delete votedAmount[msg.sender][currentEpoch];
        _safeTransferETH(msg.sender, amount);

        emit RevokeVote(msg.sender, candidateFrom, amount);
    }

    function voterWithdraw() external {
        // use epochCount, to lock votes and rewards until epoch change
        uint currentEpoch = _getAndUpdateEpochCount();
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
                address[] memory consensus = _tryGetAndCacheConsensus(epoch);
                bool included = false;
                for (uint j = 0; j < CONSENSUS_SIZE; j++) {
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
                        CONSENSUS_SIZE /
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
        // use epochCount, to lock rewards until epoch change
        uint currentEpoch = _getAndUpdateEpochCount();
        require(currentEpoch > 1, "claim not started");
        uint totalReward = 0;
        // loop all unclaimed epochs
        for (
            uint i = claimStartEpochOf[msg.sender];
            i < currentEpoch - 1;
            i++
        ) {
            // only epochs before the current running one (the one before current voting)
            address[] memory consensus = _tryGetAndCacheConsensus(i);
            bool included = false;
            for (uint j = 0; j < CONSENSUS_SIZE; j++) {
                if (consensus[j] == msg.sender) {
                    included = true;
                }
            }
            if (included) {
                totalReward +=
                    (epochRewards[i + 1] * (1000 - shareRateOf[msg.sender])) /
                    CONSENSUS_SIZE /
                    1000;
            }
        }
        claimStartEpochOf[msg.sender] = currentEpoch - 1;
        _safeTransferETH(msg.sender, totalReward);
        emit CandidateClaim(msg.sender, totalReward);
    }

    function getCurrentConsensus() public view returns (address[] memory) {
        uint epoch = getRealCurrentEpoch();
        if (epoch > 0) {
            return getConsensus(epoch - 1);
        } else {
            return getConsensus(epoch);
        }
    }

    function getConsensus(uint epoch) public view returns (address[] memory) {
        address[] memory cache = consensusCache[epoch];
        if (cache.length == 0) {
            return _computeConsensus(epoch);
        }
        return cache;
    }

    function _tryGetAndCacheConsensus(
        uint epoch
    ) internal returns (address[] memory) {
        address[] memory cache = consensusCache[epoch];
        if (cache.length == 0) {
            cache = _computeConsensus(epoch);
            consensusCache[epoch] = cache;
        }
        return cache;
    }

    function _computeConsensus(
        uint epoch
    ) internal view returns (address[] memory) {
        // build up a votes array
        address[] memory candidates = getCandidates();
        uint length = candidates.length;
        uint[] memory votes = new uint[](length);
        for (uint i = 0; i < length; i++) {
            votes[i] = receivedVotes[candidates[i]][epoch];
        }

        // sort top CONSENSUS_SIZE based on votes
        _topK(candidates, votes, CONSENSUS_SIZE);

        // return the first CONSENSUS_SIZE candidates as consensus list
        address[] memory consensus = new address[](CONSENSUS_SIZE);
        for (uint i = 0; i < CONSENSUS_SIZE; i++) {
            consensus[i] = candidates[i];
        }
        return consensus;
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }

    function _topK(
        address[] memory candidates,
        uint[] memory votes,
        uint k
    ) internal pure {
        uint length = candidates.length;
        for (int j = int(k) / 2 - 1; j >= 0; j--) {
            _heapDown(candidates, votes, uint(j), k);
        }
        for (uint i = k; i < length; i++) {
            if (votes[i] > votes[0]) {
                votes[0] = votes[i];
                candidates[0] = candidates[i];
                _heapDown(candidates, votes, 0, k);
            }
        }
    }

    function _heapDown(
        address[] memory candidates,
        uint[] memory votes,
        uint j,
        uint k
    ) internal pure {
        uint i = 2 * j + 1;
        while (i < k) {
            if (i + 1 < k && votes[i] > votes[i + 1]) {
                i += 1;
            }
            if (votes[i] > votes[j]) {
                break;
            }
            (votes[i], votes[j]) = (votes[j], votes[i]);
            (candidates[i], candidates[j]) = (candidates[j], candidates[i]);
            j = i;
            i = i * 2 + 1;
        }
    }
}
