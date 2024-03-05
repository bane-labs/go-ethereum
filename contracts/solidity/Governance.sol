// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IGovernance {
    struct Phase {
        uint startHeight;
        address[] miners;
        uint preHeight;
        uint nextHeight;
        uint voteAmount;
    }

    event Vote(address voter, address candidate, uint value);
    event RevokeVote(address voter, address candidate, uint value);

    event VotePass(
        uint votedBalance,
        uint startHeight,
        address[] miners,
        uint preHeight
    );

    event WithdrawReward(address voter, uint reward);

    // vote candidate with gas
    // add new phase when the vote condition meets
    function vote(address candidate, uint value) external payable;

    // revoke vote to candidate
    function revokeVote(address candidate) external;

    // get current consensus phase
    function getCurrentPhase() external view returns (Phase memory);

    // get consensus phase by start height
    function getPhaseByHeight(uint height) external view returns (Phase memory);

    // get reward amount of addr
    function getRewardAmount(
        address addr,
        address candidate
    ) external view returns (uint reward);

    // withdraw reward
    function withdrawReward(address candidate) external;
}

interface IGovReward {
    function withdrawERC20(address to, address token, uint amount) external;

    function withdraw(address to, uint amount) external;
}

contract Governance is IGovernance {
    // the min balance for voting
    uint public constant MIN_VOTE_AMOUNT = 1 ether;
    // the total balance target for a vote to pass
    uint public constant VOTE_TARGET_AMOUNT = 3000000 ether;
    // candidate stake amount
    uint public constant STAKE_AMOUNT = 1000 ether;
    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;
    // block count of one round
    uint public constant ROUND = 120960;

    // the last Phase's start height, default 1
    uint public lastStartHeight;

    // Phase mapping to store Phase, key is the start height of Phase,
    // should add first phase in genesis block
    mapping(uint => Phase) private phaseMap;
    // candidate mapping, candidate address -> stake height
    mapping(address => uint) private candidateMap;
    // unstake mapping, candidate address -> unstake height
    mapping(address => uint) private unSkakeMap;

    // candidate linked list
    address constant HEAD = address(1);
    mapping(address => address) _nextCandidate;
    mapping(address => address) _preCandidate;

    // vote shares mapping, phase height -> (candidate address -> (user address -> voteValue))
    mapping(uint => mapping(address => mapping(address => int)))
        private voterMap;
    // candidate vote mapping, phase height -> candidate -> voteValue
    mapping(uint => mapping(address => int)) candidateVoteMap;
    // total candidate vote mapping, candidate -> voteValue
    mapping(address => uint) totalCandidateVoteMap;
    // total voted amount
    uint private totalVotedAmount;
    // vote reward, phase height -> total reward
    mapping(uint => uint) private rewardRecord;

    // withdrawedReward reward, user address -> withdrawedReward
    mapping(address => uint) private withdrawedReward;
    // total withdrawed reward
    uint public totalWithdrawedReward;

    uint private constant _NOT_ENTERED = 1;
    uint private constant _ENTERED = 2;

    // status for nonReentrant modifier
    uint private _status;

    // ratio base
    uint public constant RatioBase = 10000;
    // vote reward ratio
    uint public constant RatioVote = 5000;

    /**
     * @dev Prevents a contract from calling itself, directly or indirectly.
     */
    modifier nonReentrant() {
        // On the first call to nonReentrant, _notEntered will be true
        require(_status != _ENTERED, "ReentrancyGuard: reentrant call");

        // Any calls to nonReentrant after this point will fail
        _status = _ENTERED;

        _;

        // By storing the original value once again, a refund is triggered (see
        // https://eips.ethereum.org/EIPS/eip-2200)
        _status = _NOT_ENTERED;
    }

    // constructor should not be in upgradable contract, this method is just for testing
    // governance contract should be pre-deployed in genesis.json
    constructor() {
        lastStartHeight = 1;
        _nextCandidate[HEAD] = HEAD;
        _preCandidate[HEAD] = HEAD;
        _status = _NOT_ENTERED;
        address[] memory defaultMiners = new address[](2);
        defaultMiners[0] = 0x625eAFa3473492007C0dD331E23B1035f6a7FB64;
        defaultMiners[1] = 0x745c8f1AF649651f46DcAEc2C6EB94068843AE96;
        phaseMap[1] = Phase({
            startHeight: 1,
            miners: defaultMiners,
            preHeight: 0,
            nextHeight: 0,
            voteAmount: 0
        });
    }

    function isMinerOfPhase(
        Phase memory phase,
        address addr
    ) public pure returns (bool) {
        for (uint i = 0; i < phase.miners.length; i++) {
            if (addr == phase.miners[i]) {
                return true;
            }
        }
        return false;
    }

    function isMiner(address addr) public view returns (bool) {
        Phase memory currentPhase = getCurrentPhase();
        return isMinerOfPhase(currentPhase, addr);
    }

    // stake GAS to be a candidate of miner
    function stake() external payable nonReentrant {
        require(msg.value >= STAKE_AMOUNT, "insufficient stake value");
        require(candidateMap[msg.sender] == 0, "already candidate");
        require(unSkakeMap[msg.sender] == 0, "already unskake");

        candidateMap[msg.sender] = block.number;

        // refund
        if (msg.value > STAKE_AMOUNT) {
            safeTransferETH(msg.sender, msg.value - STAKE_AMOUNT);
        }
    }

    // unskate GAS, the GAS can be withdrawed in next epoch
    function unstake() external {
        require(candidateMap[msg.sender] > 0, "no candidate");

        candidateMap[msg.sender] = 0;
        unSkakeMap[msg.sender] = lastStartHeight;

        // remove candidate from linked list
        _removeCandidate(msg.sender);
    }

    // claim unskated GAS
    function claimStake() external nonReentrant {
        require(unSkakeMap[msg.sender] > 0, "no unskated GAS");
        require(
            unSkakeMap[msg.sender] < lastStartHeight,
            "unskated GAS can be withdrawed in next epoch"
        );

        unSkakeMap[msg.sender] = 0;
        safeTransferETH(msg.sender, STAKE_AMOUNT);
    }

    function getVoterTotal(
        address voter,
        address candidate,
        uint phaseHeight
    ) internal view returns (uint) {
        int amount = voterMap[phaseHeight][candidate][voter];
        uint preHeight = phaseMap[phaseHeight].preHeight;
        while (preHeight > 0) {
            amount += voterMap[preHeight][candidate][voter];
            preHeight = phaseMap[preHeight].preHeight;
        }
        return uint(amount);
    }

    function getCandidateTotal(
        address candidate,
        uint phaseHeight
    ) internal view returns (uint) {
        int amount = candidateVoteMap[phaseHeight][candidate];
        uint preHeight = phaseMap[phaseHeight].preHeight;
        while (preHeight > 0) {
            amount += candidateVoteMap[preHeight][candidate];
            preHeight = phaseMap[preHeight].preHeight;
        }
        return uint(amount);
    }

    // check newValue is betweent prev and next
    function _verifyIndex(
        address prev,
        uint256 newValue,
        address next
    ) internal view returns (bool) {
        return
            (prev == HEAD || totalCandidateVoteMap[prev] >= newValue) &&
            (next == HEAD || newValue > totalCandidateVoteMap[next]);
    }

    // remove candidate
    function _removeCandidate(address candidate) internal {
        if (_nextCandidate[candidate] == address(0)) {
            return;
        }
        address pre = _preCandidate[candidate];
        address next = _nextCandidate[candidate];
        _nextCandidate[pre] = next;
        _preCandidate[next] = pre;
    }

    function updateCandidateList(address candidate, uint voteAmount) internal {
        // if candidate exists
        if (_nextCandidate[candidate] != address(0)) {
            address pre = _preCandidate[candidate];
            address next = _nextCandidate[candidate];
            // if order no need to change
            if (_verifyIndex(pre, voteAmount, next)) {
                return;
            }

            // remove candidate
            _nextCandidate[pre] = next;
            _preCandidate[next] = pre;
        }

        // if voteAmount is 0, no need to insert
        if (voteAmount == 0) {
            return;
        }
        // insert candidate
        address insertPre = _findIndex(voteAmount);
        address insertNext = _nextCandidate[insertPre];
        _nextCandidate[insertPre] = candidate;
        _nextCandidate[candidate] = insertNext;
        _preCandidate[insertNext] = candidate;
        _preCandidate[candidate] = insertPre;
    }

    function _findIndex(uint voteAmount) internal view returns (address) {
        address candidateAddress = HEAD;
        while (_nextCandidate[candidateAddress] != HEAD) {
            if (
                _verifyIndex(
                    candidateAddress,
                    voteAmount,
                    _nextCandidate[candidateAddress]
                )
            ) {
                return candidateAddress;
            }
            candidateAddress = _nextCandidate[candidateAddress];
        }
        return candidateAddress;
    }

    function getTopCandidates(
        uint256 k
    ) public view returns (address[] memory) {
        address[] memory result = new address[](k);
        address currentAddress = _nextCandidate[HEAD];
        uint i = 0;
        while (currentAddress != HEAD && i < k) {
            result[i] = currentAddress;
            currentAddress = _nextCandidate[currentAddress];
            i++;
        }
        return result;
    }

    function _checkVotePass(uint votedTotal) internal view returns (bool) {
        // check round
        if ((block.number - lastStartHeight) < ROUND) {
            return false;
        }
        if (votedTotal < VOTE_TARGET_AMOUNT) {
            return false;
        }
        address[] memory miners = getTopCandidates(7);
        if (miners[6] == address(0)) {
            return false;
        }
        return true;
    }

    function vote(
        address candidate,
        uint amount
    ) external payable override nonReentrant {
        require(candidateMap[candidate] > 0, "invalid candidate");
        require(amount >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(msg.value >= amount, "insufficient value");

        // add this vote in this phase
        voterMap[lastStartHeight][candidate][msg.sender] += int(amount);
        candidateVoteMap[lastStartHeight][candidate] += int(amount);
        totalCandidateVoteMap[candidate] += amount;
        totalVotedAmount += amount;
        updateCandidateList(candidate, totalCandidateVoteMap[candidate]);
        emit Vote(msg.sender, candidate, amount);

        // check total vote balance
        if (_checkVotePass(totalVotedAmount)) {
            Phase memory phase = Phase({
                startHeight: block.number + 1,
                miners: getTopCandidates(7),
                preHeight: lastStartHeight,
                nextHeight: 0,
                voteAmount: totalVotedAmount
            });
            phaseMap[lastStartHeight].nextHeight = phase.startHeight;
            phaseMap[phase.startHeight] = phase;
            lastStartHeight = phase.startHeight;

            // record current reward balance
            rewardRecord[phase.startHeight] =
                govReward.balance +
                totalWithdrawedReward;

            emit VotePass(
                totalVotedAmount,
                phase.startHeight,
                phase.miners,
                phase.preHeight
            );
        }

        // refund
        if (msg.value > amount) {
            safeTransferETH(msg.sender, msg.value - amount);
        }
    }

    function safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }

    function revokeVote(address candidate) external override nonReentrant {
        uint voteValue = getVoterTotal(msg.sender, candidate, lastStartHeight);
        require(voteValue > 0, "empty vote value");

        voterMap[lastStartHeight][candidate][msg.sender] -= int(voteValue);
        candidateVoteMap[lastStartHeight][candidate] -= int(voteValue);
        totalCandidateVoteMap[candidate] -= voteValue;
        totalVotedAmount -= voteValue;

        safeTransferETH(msg.sender, voteValue);
        emit RevokeVote(msg.sender, candidate, voteValue);
    }

    function getRewardAmount(
        address addr,
        address candidate
    ) public view returns (uint reward) {
        Phase memory current = phaseMap[lastStartHeight];
        Phase memory prePhase = phaseMap[current.preHeight];
        uint currentReward = 0;
        uint preReward = 0;
        // the first phase, there is no reward
        while (current.startHeight > 1 && isMinerOfPhase(current, candidate)) {
            uint share = getVoterTotal(addr, candidate, current.preHeight);
            uint candidateTotal = getCandidateTotal(
                candidate,
                current.preHeight
            );
            // for the last phase, we calculate the balance of govReward
            if (current.startHeight == lastStartHeight) {
                currentReward =
                    govReward.balance +
                    totalWithdrawedReward -
                    rewardRecord[current.startHeight];
            } else {
                // for pre phases, we get by rewardRecord
                currentReward = preReward;
            }
            // sum all the vote reward for addr
            reward +=
                (currentReward * share * RatioVote) /
                candidateTotal /
                current.miners.length /
                RatioBase;

            // sum all the consensus reward
            if (isMinerOfPhase(current, addr)) {
                reward +=
                    (currentReward * (RatioBase - RatioVote)) /
                    current.miners.length /
                    RatioBase;
                break;
            }

            // calculate the reward for pre phase
            preReward =
                rewardRecord[current.startHeight] -
                rewardRecord[prePhase.startHeight];

            current = prePhase;
            prePhase = phaseMap[current.preHeight];
        }
        // total reward - withdrawed reward
        reward -= withdrawedReward[addr];
        return reward;
    }

    function withdrawReward(address candidate) external nonReentrant {
        uint reward = getRewardAmount(msg.sender, candidate);
        if (reward > 0) {
            withdrawedReward[msg.sender] += reward;
            totalWithdrawedReward += reward;
            IGovReward(govReward).withdraw(msg.sender, reward);
            emit WithdrawReward(msg.sender, reward);
        }
    }

    function getCurrentPhase() public view override returns (Phase memory) {
        return getPhaseByHeight(block.number);
    }

    function getPhaseByHeight(
        uint height
    ) public view override returns (Phase memory) {
        if (height >= lastStartHeight) {
            return phaseMap[lastStartHeight];
        }
        uint currentHeight = lastStartHeight;
        while (height < currentHeight) {
            currentHeight = phaseMap[currentHeight].preHeight;
        }
        return phaseMap[currentHeight];
    }
}
