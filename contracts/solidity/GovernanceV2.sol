// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";

interface IGovernanceV2 {
    event Register(address candidate);
    event Exit(address candidate);
    event Vote(address voter, address to, uint amount);
    event Revoke(address voter, address from, uint amount);
    event VoterClaim(address voter, uint reward);
    event CandidateWithdraw(address candidate, uint amount);
    event Persist(address[] validators);

    // register to be a candidate with gas
    function registerCandidate(uint shareRate) external payable;

    // exit candidates and wait for withdraw
    function exitCandidate() external;

    // withdraw register fee after 2 epoch
    function withdrawRegisterFee() external;

    // vote with gas, only 1 target is allowed
    function vote(address to) external payable;

    // revoke votes and claim rewards
    function revokeVote() external;

    // only claim rewards
    function claimReward() external;

    /*
        The following should only be used by DBFT module, refer to https://github.com/nspcc-dev/neo-go/blob/master/pkg/core/blockchain.go
    */

    // get the consensus group before postPersist, which is the group that should produce this block
    function getNextBlockValidators() external returns (address[] memory);

    // select the latest consensus group after postPersist, which is the group that should produce the next block
    function computeNextBlockValidators() external returns (address[] memory);

    // compute and update cached consensus group
    function postPersist() external;
}

interface IGovReward {
    function withdraw() external;
}

contract GovernanceV2 is IGovernanceV2 {
    using EnumerableSet for EnumerableSet.AddressSet;

    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;
    uint public constant SCALE_FACTOR = 10 ** 18;

    uint public CONSENSUS_SIZE;
    // the min balance for voting
    uint public MIN_VOTE_AMOUNT;
    // register fee
    uint public REGISTER_FEE;
    // duration of an epoch (in blocks)
    uint public EPOCH_DURATION;

    // candidate list
    EnumerableSet.AddressSet internal candidateList;
    // settings about how much reward given to voter
    mapping(address => uint) public shareRateOf;
    // the height when exit happens
    mapping(address => uint) public exitHeightOf;
    // the left register fee to exit
    mapping(address => uint) public candidateBalanceOf;

    // candidate=>amount
    mapping(address => uint) public receivedVotes;
    // voter=>candidate
    mapping(address => address) public votedTo;
    // voter=>amount
    mapping(address => uint) public votedAmount;

    // the block height when current epoch starts
    uint public currentEpochStartHeight;
    // the current group of block validators
    address[] public currentConsensus;

    // candidate=>total
    mapping(address => uint) public candidateGasPerVote;
    // voter=>number
    mapping(address => uint) public voterGasPerVote;
    // voter=>height
    mapping(address => uint) public voteHeight;
    // candidate=>height=>number
    mapping(address => mapping(uint => uint)) public epochStartGasPerVote;

    receive() external payable {
        address[] memory validators = currentConsensus;
        uint length = validators.length;
        for (uint i = 0; i < length; i++) {
            candidateGasPerVote[validators[i]] +=
                (msg.value * shareRateOf[validators[i]] * SCALE_FACTOR) /
                7 /
                1000 /
                receivedVotes[validators[i]];
            _safeTransferETH(
                validators[i],
                (msg.value * (1000 - shareRateOf[validators[i]])) / 7 / 1000
            );
        }
    }

    function getCandidates() public view returns (address[] memory) {
        return candidateList.values();
    }

    function registerCandidate(uint shareRate) external payable {
        require(msg.value == REGISTER_FEE, "insufficient amount");
        require(shareRate < 1000, "invalid rate");
        require(!candidateList.contains(msg.sender), "candidate exists");
        require(exitHeightOf[msg.sender] == 0, "left not claimed");
        candidateList.add(msg.sender);

        // record share rate and balance
        shareRateOf[msg.sender] = shareRate;
        candidateBalanceOf[msg.sender] = msg.value;
        emit Register(msg.sender);
    }

    function exitCandidate() external {
        require(candidateList.contains(msg.sender), "candidate not exists");
        // remove candidate list, balance still locked
        candidateList.remove(msg.sender);
        exitHeightOf[msg.sender] = block.number;
        emit Exit(msg.sender);
    }

    function withdrawRegisterFee() external {
        // require 2 epochs to exit candidate list
        // NOTE: suppose postPersist always happens in time
        require(
            exitHeightOf[msg.sender] > 0 &&
                block.number > exitHeightOf[msg.sender] + 2 * EPOCH_DURATION,
            "withdraw not allowed"
        );

        // send back balance
        uint amount = candidateBalanceOf[msg.sender];
        delete candidateBalanceOf[msg.sender];
        delete exitHeightOf[msg.sender];
        _safeTransferETH(msg.sender, amount);
        emit CandidateWithdraw(msg.sender, amount);
    }

    function vote(address candidateTo) external payable {
        require(msg.value >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(candidateList.contains(candidateTo), "candidate not allowed");
        address votedCandidate = votedTo[msg.sender];
        require(
            votedCandidate == candidateTo || votedCandidate == address(0),
            "only one choice is allowed"
        );

        // settle reward here
        if (votedCandidate != address(0)) {
            _settleReward(msg.sender, votedCandidate);
        } else {
            // record tag value
            votedTo[msg.sender] = candidateTo;
            voterGasPerVote[msg.sender] = candidateGasPerVote[candidateTo];
        }

        // update votes
        votedAmount[msg.sender] += msg.value;
        receivedVotes[candidateTo] += msg.value;
        // NOTE: the left reward in current epoch will be unclaimable
        voteHeight[msg.sender] = block.number;

        emit Vote(msg.sender, candidateTo, msg.value);
    }

    function revokeVote() external {
        address candidateFrom = votedTo[msg.sender];
        uint amount = votedAmount[msg.sender];
        require(
            candidateFrom != address(0) && amount > 0,
            "revoke not allowed"
        );

        // settle reward here
        _settleReward(msg.sender, candidateFrom);

        // update votes
        receivedVotes[candidateFrom] -= amount;
        delete votedTo[msg.sender];
        delete votedAmount[msg.sender];

        // delete tag value
        delete voterGasPerVote[msg.sender];
        delete voteHeight[msg.sender];

        _safeTransferETH(msg.sender, amount);
        emit Revoke(msg.sender, candidateFrom, amount);
    }

    function claimReward() external {
        address votedCandidate = votedTo[msg.sender];
        require(votedCandidate != address(0), "claim not allowed");
        _settleReward(msg.sender, votedCandidate);
    }

    function postPersist() external {
        // NOTE: suppose postPersist always happens when equal
        require(
            block.number >= currentEpochStartHeight + EPOCH_DURATION,
            "persist not allowed"
        );
        IGovReward(govReward).withdraw();

        currentEpochStartHeight = block.number;
        currentConsensus = _computeConsensus();
        uint length = currentConsensus.length;
        for (uint i = 0; i < length; i++) {
            epochStartGasPerVote[currentConsensus[i]][
                currentEpochStartHeight / EPOCH_DURATION
            ] = candidateGasPerVote[currentConsensus[i]];
        }
        emit Persist(currentConsensus);
    }

    function getNextBlockValidators() external view returns (address[] memory) {
        return currentConsensus;
    }

    function computeNextBlockValidators()
        external
        view
        returns (address[] memory)
    {
        return _computeConsensus();
    }

    function _settleReward(address voter, address candidate) internal {
        IGovReward(govReward).withdraw();

        uint height = voteHeight[voter];
        uint lastGasPerVote = voterGasPerVote[voter];
        uint latestGasPerVote = candidateGasPerVote[candidate];

        // NOTE: suppose postPersist always happens in the correct block at expected height
        // NOTE: suppose postPersist always happens at the beginning of a block, then vote in that block should wait another epoch to farm reward
        uint voteEpochEndGasPerVote = epochStartGasPerVote[candidate][
            (height - 1) / EPOCH_DURATION + 1
        ];
        if (voteEpochEndGasPerVote > lastGasPerVote) {
            lastGasPerVote = voteEpochEndGasPerVote;
        }

        uint reward = (votedAmount[voter] *
            (latestGasPerVote - lastGasPerVote)) / SCALE_FACTOR;
        voterGasPerVote[voter] = latestGasPerVote;
        _safeTransferETH(voter, reward);
        emit VoterClaim(voter, reward);
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }

    function _computeConsensus() internal view returns (address[] memory) {
        // build up a votes array
        address[] memory candidates = getCandidates();
        uint length = candidates.length;
        uint[] memory votes = new uint[](length);
        for (uint i = 0; i < length; i++) {
            votes[i] = receivedVotes[candidates[i]];
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
