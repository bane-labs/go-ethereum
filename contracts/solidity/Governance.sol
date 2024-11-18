// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {IGovReward} from "./interfaces/IGovReward.sol";
import {IKeyManagement} from "./interfaces/IKeyManagement.sol";
import {IGovernance} from "./interfaces/IGovernance.sol";
import {IPolicy} from "./interfaces/IPolicy.sol";
import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";
import {EnumerableSet} from "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

contract Governance is IGovernance, ReentrancyGuard, GovProxyUpgradeable {
    using EnumerableSet for EnumerableSet.AddressSet;

    address public constant SELF = 0x1212100000000000000000000000000000000001;
    // Policy contract
    address public constant POLICY = 0x1212000000000000000000000000000000000002;
    // GovReward contract
    address public constant GOV_REWARD =
        0x1212000000000000000000000000000000000003;
    address public constant KEY_MANAGEMENT =
        0x1212000000000000000000000000000000000008;
    address public constant SYS_CALL =
        0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE;
    uint public constant SCALE_FACTOR = 10 ** 18;
    uint public constant EXIT_FEE_RATE = 50;

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
    // voter=>height, deprecated
    mapping(address => uint) public voteHeight;
    // candidate=>height=>number
    mapping(address => mapping(uint => uint)) public epochStartGasPerVote;
    // blacklisted candidate amount
    uint public blacklistedCandidates;

    // duration of each secret share period (in blocks)
    uint public sharePeriodDuration;
    // the pending group of block validators
    address[] public pendingConsensus;

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

    // Only for Governance upgrading for the new KeyManagement contract
    function setInitialSharePeriodDuration(uint _sharePeriodDuration) external onlyAdmin {
        sharePeriodDuration = _sharePeriodDuration;
    }

    receive() external payable nonReentrant {
        if (msg.sender != GOV_REWARD) revert Errors.SideCallNotAllowed();
        address[] memory validators = currentConsensus;
        uint length = validators.length;
        for (uint i = 0; i < length; i++) {
            uint shareRate = shareRateOf[validators[i]];
            uint voteAmount = receivedVotes[validators[i]];
            if (voteAmount != 0) {
                candidateGasPerVote[validators[i]] +=
                    (msg.value * shareRate * SCALE_FACTOR) /
                    (consensusSize * 1000 * voteAmount);
            }
            if (shareRate < 1000) {
                _safeTransferETH(
                    validators[i],
                    (msg.value * (1000 - shareRate)) / (consensusSize * 1000)
                );
            }
        }
    }

    function getCandidates() public view returns (address[] memory) {
        return candidateList.values();
    }

    function registerCandidate(uint shareRate) external payable {
        if (tx.origin != msg.sender) revert Errors.OnlyEOA();
        if (msg.value != registerFee) revert Errors.InsufficientValue();
        if (shareRate > 1000) revert Errors.InvalidShareRate();
        if (
            candidateList.length() + blacklistedCandidates >=
            IPolicy(POLICY).getCandidateLimit()
        ) revert Errors.RegisterDisabled();
        if (exitHeightOf[msg.sender] > 0) revert Errors.LeftNotClaimed();
        if (!_activateCandidate(msg.sender)) revert Errors.CandidateExists();

        // record share rate and balance
        shareRateOf[msg.sender] = shareRate;
        candidateBalanceOf[msg.sender] = msg.value;
    }

    function exitCandidate() external {
        if (!_deactivateCandidate(msg.sender))
            revert Errors.CandidateNotExists();
    }

    function withdrawRegisterFee() external nonReentrant {
        // require 2 epochs to exit candidate list
        // NOTE: suppose epoch change always happens in time
        if (
            exitHeightOf[msg.sender] <= 0 ||
            block.number <= exitHeightOf[msg.sender] + 2 * epochDuration
        ) revert Errors.CandidateWithdrawNotAllowed();

        // send back balance
        uint amount = (candidateBalanceOf[msg.sender] * (100 - EXIT_FEE_RATE)) /
            100;
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

        emit Vote(msg.sender, candidateTo, msg.value);
        if (unclaimedReward > 0) _safeTransferETH(msg.sender, unclaimedReward);
    }

    function revokeVote(uint amount) external nonReentrant {
        address candidateFrom = votedTo[msg.sender];
        uint leftAmount = votedAmount[msg.sender];
        if (candidateFrom == address(0) || leftAmount <= 0)
            revert Errors.NoVote();
        if (amount > leftAmount) revert Errors.InsufficientValue();

        // settle reward here
        uint unclaimedReward = _settleReward(msg.sender, candidateFrom);

        // update votes
        receivedVotes[candidateFrom] -= amount;
        // only decrease totalVotes for active candidate
        if (candidateList.contains(candidateFrom)) {
            totalVotes -= amount;
        }
        if (amount == leftAmount) {
            // delete tag value
            delete votedTo[msg.sender];
            delete votedAmount[msg.sender];
            delete voterGasPerVote[msg.sender];
            delete voteHeight[msg.sender];
        } else {
            votedAmount[msg.sender] -= amount;
        }

        emit Revoke(msg.sender, candidateFrom, amount);
        _safeTransferETH(msg.sender, amount + unclaimedReward);
    }

    function transferVote(address candidateTo) external nonReentrant {
        address candidateFrom = votedTo[msg.sender];
        uint amount = votedAmount[msg.sender];
        if (candidateFrom == address(0) || amount <= 0) revert Errors.NoVote();
        if (candidateFrom == candidateTo) revert Errors.SameCandidate();
        if (!candidateList.contains(candidateTo))
            revert Errors.CandidateNotExists();

        // settle reward here
        uint unclaimedReward = _settleReward(msg.sender, candidateFrom);

        // update votes
        receivedVotes[candidateFrom] -= amount;
        receivedVotes[candidateTo] += amount;
        if (!candidateList.contains(candidateFrom)) {
            totalVotes += amount;
        }
        votedTo[msg.sender] = candidateTo;
        voterGasPerVote[msg.sender] = candidateGasPerVote[candidateTo];

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

    function onPersistV2() external {
        // NOTE: suppose onPersist always happens at the beginning of every block
        if (msg.sender != SYS_CALL) revert Errors.SideCallNotAllowed();
        // only settle validator reward if there is no epoch change
        IGovReward(GOV_REWARD).withdraw();
        if (
            block.number <
            currentEpochStartHeight + epochDuration - 2 * sharePeriodDuration
        ) return;

        // settle vote epoch as pending but keep the running consensus
        address[] memory candidates = candidateList.values();
        uint length = candidates.length;
        if (pendingConsensus.length == 0) {
            // compute and update pending consensus
            if (length < consensusSize || totalVotes < voteTargetAmount) {
                pendingConsensus = standByValidators;
            } else {
                pendingConsensus = _computeConsensus(candidates);
            }
            emit Persist(pendingConsensus);
        }
        if (block.number < currentEpochStartHeight + epochDuration) return;

        // set pending consensus as running, and update tag values
        currentEpochStartHeight = block.number;
        for (uint i = 0; i < length; i++) {
            epochStartGasPerVote[candidates[i]][
                block.number / epochDuration
            ] = candidateGasPerVote[candidates[i]];
        }
        // check if key generation succeeds, keep the same members if not
        if (IKeyManagement(KEY_MANAGEMENT).isCurrentRoundReady()) {
            currentConsensus = pendingConsensus;
        }
        // reset pending value and start a new epoch
        delete pendingConsensus;
        emit Persist(currentConsensus);
    }

    function activateCandidate(address candidate) external {
        if (msg.sender != POLICY) revert Errors.SideCallNotAllowed();
        if (exitHeightOf[candidate] > 0 && _activateCandidate(candidate))
            blacklistedCandidates -= 1;
    }

    function deactivateCandidate(address candidate) external {
        if (msg.sender != POLICY) revert Errors.SideCallNotAllowed();
        if (_deactivateCandidate(candidate)) blacklistedCandidates += 1;
    }

    function getCurrentConsensus() public view returns (address[] memory) {
        return currentConsensus;
    }

    function getPendingConsensus() public view returns (address[] memory) {
        return pendingConsensus;
    }

    function _activateCandidate(address candidate) internal returns (bool) {
        if (!candidateList.add(candidate)) return false;
        delete exitHeightOf[candidate];
        if (receivedVotes[candidate] > 0) {
            totalVotes += receivedVotes[candidate];
        }
        emit Activate(candidate);
        return true;
    }

    function _deactivateCandidate(address candidate) internal returns (bool) {
        if (!candidateList.remove(candidate)) return false;
        // remove candidate list, balance still locked
        exitHeightOf[candidate] = block.number;
        if (receivedVotes[candidate] > 0) {
            totalVotes -= receivedVotes[candidate];
        }
        emit Deactivate(candidate);
        return true;
    }

    function _computeReward(
        address voter,
        address candidate
    ) internal view returns (uint) {
        // NOTE: suppose onPersist always happens at the beginning of every block, then latestGasPerVote is always the latest
        uint lastGasPerVote = voterGasPerVote[voter];
        uint latestGasPerVote = candidateGasPerVote[candidate];
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
