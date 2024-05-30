// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "./Errors.sol";
import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

interface IGovernance {
    event Register(address candidate);
    event Exit(address candidate);
    event Vote(address indexed voter, address indexed to, uint amount);
    event Revoke(address indexed voter, address indexed from, uint amount);
    event VoterClaim(address indexed voter, uint reward);
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

    // revoke votes, claim rewards and vote to another candidate
    function transferVote(address candidateTo) external;

    // only claim rewards
    function claimReward() external;

    // get reward amount to be claimed when settle
    function unclaimedRewardOf(address voter) external view returns (uint);

    // get consensus group members
    function getCurrentConsensus() external view returns (address[] memory);

    // compute and update cached consensus group
    function onPersist() external;
}

interface IGovReward {
    function withdraw() external;
}

contract Governance is IGovernance, ReentrancyGuard, UUPSUpgradeable {
    using EnumerableSet for EnumerableSet.AddressSet;

    address public constant SELF = 0x1212100000000000000000000000000000000001;
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;
    // GovReward contract
    address public constant GOV_REWARD =
        0x1212000000000000000000000000000000000003;
    address public constant SYS_CALL =
        0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE;
    uint public constant SCALE_FACTOR = 10 ** 18;

    uint public consensusSize;
    // the min balance for voting
    uint public minVoteAmount;
    // the min amount to make vote result valid
    uint public voteTargetAmount;
    // register fee
    uint public registerFee;
    // duration of an epoch (in blocks)
    uint public epochDuration;

    // candidate list
    EnumerableSet.AddressSet internal candidateList;
    // settings about how much reward given to voter
    mapping(address => uint) public shareRateOf;
    // the height when exit happens
    mapping(address => uint) public exitHeightOf;
    // the left register fee to exit
    mapping(address => uint) public candidateBalanceOf;

    uint public totalVotes;
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
    // a fixed list of stand-by validators to be selected as consensus
    address[] public standByValidators;

    // candidate=>total
    mapping(address => uint) public candidateGasPerVote;
    // voter=>number
    mapping(address => uint) public voterGasPerVote;
    // voter=>height
    mapping(address => uint) public voteHeight;
    // candidate=>height=>number
    mapping(address => mapping(uint => uint)) public epochStartGasPerVote;

    modifier onlyAdmin() {
        if (msg.sender != GOV_ADMIN) revert Errors.NotAdmin();
        _;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}

    // Only for precompiled uups implementation in genesis file, need to be removed when upgrading the contract.
    // This override is added because "immutable __self" in UUPSUpgradeable is not avaliable in precompiled contract.
    function _checkProxy() internal view virtual override {
        if (
            address(this) == SELF || // Must be called through delegatecall
            ERC1967Utils.getImplementation() != SELF // Must be called through an active proxy
        ) {
            revert UUPSUnauthorizedCallContext();
        }
    }

    // Only for precompiled uups implementation in genesis file, need to be removed when upgrading the contract.
    // This override is added because "immutable __self" in UUPSUpgradeable is not avaliable in precompiled contract.
    function _checkNotDelegated() internal view virtual override {
        if (address(this) != SELF) {
            // Must not be called through delegatecall
            revert UUPSUnauthorizedCallContext();
        }
    }

    receive() external payable nonReentrant {
        if (msg.sender != GOV_REWARD) revert Errors.SideCallNotAllowed();
        address[] memory validators = currentConsensus;
        uint length = validators.length;
        for (uint i = 0; i < length; i++) {
            if (receivedVotes[validators[i]] != 0) {
                candidateGasPerVote[validators[i]] +=
                    (msg.value * shareRateOf[validators[i]] * SCALE_FACTOR) /
                    consensusSize /
                    1000 /
                    receivedVotes[validators[i]];
            }
            _safeTransferETH(
                validators[i],
                (msg.value * (1000 - shareRateOf[validators[i]])) /
                    consensusSize /
                    1000
            );
        }
    }

    function getCandidates() public view returns (address[] memory) {
        return candidateList.values();
    }

    function registerCandidate(uint shareRate) external payable {
        if (tx.origin != msg.sender) revert Errors.OnlyEOA();
        if (msg.value < registerFee) revert Errors.InsufficientValue();
        if (shareRate > 1000) revert Errors.InvalidShareRate();
        if (exitHeightOf[msg.sender] > 0) revert Errors.LeftNotClaimed();
        if (!candidateList.add(msg.sender)) revert Errors.CandidateExists();
        if (receivedVotes[msg.sender] > 0) {
            totalVotes += receivedVotes[msg.sender];
        }

        // record share rate and balance
        shareRateOf[msg.sender] = shareRate;
        candidateBalanceOf[msg.sender] = msg.value;
        emit Register(msg.sender);
    }

    function exitCandidate() external {
        if (!candidateList.remove(msg.sender))
            revert Errors.CandidateNotExists();
        // remove candidate list, balance still locked
        exitHeightOf[msg.sender] = block.number;
        if (receivedVotes[msg.sender] > 0) {
            totalVotes -= receivedVotes[msg.sender];
        }
        emit Exit(msg.sender);
    }

    function withdrawRegisterFee() external nonReentrant {
        // require 2 epochs to exit candidate list
        // NOTE: suppose epoch change always happens in time
        if (
            exitHeightOf[msg.sender] <= 0 ||
            block.number <= exitHeightOf[msg.sender] + 2 * epochDuration
        ) revert Errors.CandidateWithdrawNotAllowed();

        // send back balance
        uint amount = candidateBalanceOf[msg.sender];
        delete candidateBalanceOf[msg.sender];
        delete exitHeightOf[msg.sender];
        delete shareRateOf[msg.sender];

        emit CandidateWithdraw(msg.sender, amount);
        _safeTransferETH(msg.sender, amount);
    }

    function vote(address candidateTo) external payable nonReentrant {
        if (msg.value < minVoteAmount) revert Errors.InsufficientValue();
        if (!candidateList.contains(candidateTo))
            revert Errors.CandidateNotExists();
        address votedCandidate = votedTo[msg.sender];
        if (votedCandidate != candidateTo && votedCandidate != address(0))
            revert Errors.MultipleVoteNotAllowed();

        // settle reward here
        uint unclaimedReward = 0;
        if (votedCandidate != address(0)) {
            unclaimedReward = _settleReward(msg.sender, votedCandidate);
        } else {
            // record tag value
            votedTo[msg.sender] = candidateTo;
            voterGasPerVote[msg.sender] = candidateGasPerVote[candidateTo];
        }

        // update votes
        votedAmount[msg.sender] += msg.value;
        receivedVotes[candidateTo] += msg.value;
        totalVotes += msg.value;
        // NOTE: the left reward in the first epoch of first vote will be unclaimable.
        if (votedCandidate == address(0)) {
            voteHeight[msg.sender] = block.number;
        }

        emit Vote(msg.sender, candidateTo, msg.value);
        if (unclaimedReward > 0) _safeTransferETH(msg.sender, unclaimedReward);
    }

    function revokeVote() external nonReentrant {
        address candidateFrom = votedTo[msg.sender];
        uint amount = votedAmount[msg.sender];
        if (candidateFrom == address(0) || amount <= 0) revert Errors.NoVote();

        // settle reward here
        uint unclaimedReward = _settleReward(msg.sender, candidateFrom);

        // update votes
        receivedVotes[candidateFrom] -= amount;
        // only decrease totalVotes for active candidate
        if (candidateList.contains(candidateFrom)) {
            totalVotes -= amount;
        }
        delete votedTo[msg.sender];
        delete votedAmount[msg.sender];

        // delete tag value
        delete voterGasPerVote[msg.sender];
        delete voteHeight[msg.sender];

        emit Revoke(msg.sender, candidateFrom, amount);
        _safeTransferETH(msg.sender, amount + unclaimedReward);
    }

    function transferVote(address candidateTo) external nonReentrant {
        address candidateFrom = votedTo[msg.sender];
        uint amount = votedAmount[msg.sender];
        require(candidateFrom != address(0) && amount > 0, "no vote to change");
        require(candidateFrom != candidateTo, "voting to the same candidate");
        require(candidateList.contains(candidateTo), "candidate not allowed");

        // settle reward here
        uint unclaimedReward = _settleReward(msg.sender, candidateFrom);

        // update votes
        receivedVotes[candidateFrom] -= amount;
        receivedVotes[candidateTo] += amount;
        votedTo[msg.sender] = candidateTo;
        voterGasPerVote[msg.sender] = candidateGasPerVote[candidateTo];
        voteHeight[msg.sender] = block.number;

        emit Revoke(msg.sender, candidateFrom, amount);
        emit Vote(msg.sender, candidateTo, amount);
        if (unclaimedReward > 0) _safeTransferETH(msg.sender, unclaimedReward);
    }

    function claimReward() external nonReentrant {
        address votedCandidate = votedTo[msg.sender];
        if (votedCandidate == address(0)) revert Errors.NoVote();
        uint unclaimedReward = _settleReward(msg.sender, votedCandidate);
        if (unclaimedReward > 0) _safeTransferETH(msg.sender, unclaimedReward);
    }

    function unclaimedRewardOf(address voter) external view returns (uint) {
        address votedCandidate = votedTo[voter];
        if (votedCandidate == address(0)) return 0;
        else return _computeReward(voter, votedCandidate);
    }

    function onPersist() external {
        // NOTE: suppose onPersist always happens at the beginning of every block
        if (msg.sender != SYS_CALL) revert Errors.SideCallNotAllowed();
        // only settle validator reward if there is no epoch change
        IGovReward(GOV_REWARD).withdraw();
        if (block.number < currentEpochStartHeight + epochDuration) return;

        // update tag values
        currentEpochStartHeight = block.number;
        address[] memory candidates = candidateList.values();
        uint length = candidates.length;
        for (uint i = 0; i < length; i++) {
            epochStartGasPerVote[candidates[i]][
                block.number / epochDuration
            ] = candidateGasPerVote[candidates[i]];
        }

        // compute and update consensus
        if (length < consensusSize || totalVotes < voteTargetAmount) {
            currentConsensus = standByValidators;
        } else {
            currentConsensus = _computeConsensus(candidates);
        }
        emit Persist(currentConsensus);
    }

    function getCurrentConsensus() public view returns (address[] memory) {
        return currentConsensus;
    }

    function _computeReward(
        address voter,
        address candidate
    ) internal view returns (uint) {
        // NOTE: suppose onPersist always happens at the beginning of every block, then latestGasPerVote is always the latest
        uint height = voteHeight[voter];
        if (currentEpochStartHeight <= height) return 0;
        uint lastGasPerVote = voterGasPerVote[voter];
        uint latestGasPerVote = candidateGasPerVote[candidate];

        // NOTE: suppose epoch change always happens at the beginning of a block, then vote in that block should wait another epoch to farm reward
        uint voteEpochEndGasPerVote = epochStartGasPerVote[candidate][
            height / epochDuration + 1
        ];
        if (voteEpochEndGasPerVote > lastGasPerVote) {
            lastGasPerVote = voteEpochEndGasPerVote;
        }

        return
            (votedAmount[voter] * (latestGasPerVote - lastGasPerVote)) /
            SCALE_FACTOR;
    }

    function _settleReward(
        address voter,
        address candidate
    ) internal returns (uint) {
        uint reward = _computeReward(voter, candidate);
        voterGasPerVote[voter] = candidateGasPerVote[candidate];
        emit VoterClaim(voter, reward);
        return reward;
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        if (!success) revert Errors.TransferFailed();
    }

    function _computeConsensus(
        address[] memory candidates
    ) internal view returns (address[] memory) {
        // build up a votes array
        uint length = candidates.length;
        uint[] memory votes = new uint[](length);
        for (uint i = 0; i < length; i++) {
            votes[i] = receivedVotes[candidates[i]];
        }

        // sort top consensusSize based on votes
        _topK(candidates, votes, consensusSize);

        // return the first consensusSize candidates as consensus list
        address[] memory consensus = new address[](consensusSize);
        for (uint i = 0; i < consensusSize; i++) {
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
