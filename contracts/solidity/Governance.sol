// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IGovernance {
    struct Draft {
        uint id;
        uint startHeight;
        address[] miners;
    }

    struct Phase {
        uint startHeight;
        address[] miners;
        uint preHeight;
        uint draftId;
        uint voteAmount;
    }

    event Propose(
        address proposer,
        uint draftId,
        uint startHeight,
        address[] miners
    );

    event Vote(address voter, uint draftId, uint value);
    event RevokeVote(address voter, uint draftId, uint value);

    event VotePass(
        uint votedBalance,
        uint startHeight,
        address[] miners,
        uint preHeight
    );

    event WithdrawReward(address voter, uint reward);

    // propose draft, contains start height and consensus list
    function propose(uint startHeight, address[] memory miners) external;

    // get Draft list, contians unique id
    function getDraftList() external view returns (Draft[] memory);

    // vote draft with gas
    // when the vote condition meets: 1. convert draft to phase; 2.clean draft list
    function vote(uint draftId, uint value) external payable;

    // revoke vote
    function revokeVote(uint draftId) external;

    // get current consensus phase
    function getCurrentPhase() external view returns (Phase memory);

    // get consensus phase by start height
    function getPhaseByHeight(uint height) external view returns (Phase memory);

    // get reward amount of addr
    function getRewardAmount(address addr) external view returns (uint reward);

    // withdraw reward
    function withdrawReward() external;
}

interface IGovReward {
    function withdrawERC20(address to, address token, uint amount) external;

    function withdraw(address to, uint amount) external;
}

contract Governance is IGovernance {
    // the min balance for voting
    uint public constant MIN_VOTE_AMOUNT = 1 ether;
    // the balance target for a vote to pass
    uint public constant VOTE_TARGET_AMOUNT = 3000000 ether;
    // GovReward contract
    address public constant govReward =
        0x1212000000000000000000000000000000000003;

    // the last Phase's start height, default 1
    uint public lastStartHeight;
    // draft list start id, default 1
    uint public startDraftId;
    // draft list end id, default 0
    uint public endDraftId;

    // Phase mapping to store Phase, key is the start height of Phase,
    // should add first phase in genesis block
    mapping(uint => Phase) private phaseMap;
    // Draft mapping, draftId -> Draft
    mapping(uint => Draft) private draftMap;

    // vote user mapping, draftId -> (user address -> voteValue)
    mapping(uint => mapping(address => uint)) private draftVoterMap;
    // vote draft value mapping, draftId -> voteValue
    mapping(uint => uint) private draftValueMap;

    // vote shares mapping, draftId -> (user address -> voteValue)
    mapping(uint => mapping(address => uint)) private rewardShareMap;
    // vote reward, startHeight -> total reward
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

    modifier onlyConsensus() {
        require(isMiner(msg.sender), "sender is not a consensus member");
        _;
    }

    function isMiner(address addr) public view returns (bool) {
        Phase memory currentPhase = getCurrentPhase();
        for (uint i = 0; i < currentPhase.miners.length; i++) {
            if (addr == currentPhase.miners[i]) {
                return true;
            }
        }
        return false;
    }

    function propose(
        uint startHeight,
        address[] memory miners
    ) external override onlyConsensus {
        require(startHeight > block.number, "invalid startHeight");
        require(miners.length > 0, "invalid miners lenght");
        // check miners order
        if (miners.length > 1) {
            for (uint i = 0; i < miners.length - 1; i++) {
                require(miners[i] < miners[i + 1], "invalid miners order");
            }
        }

        require(
            block.number > lastStartHeight,
            "propose should be called after last phase start"
        );

        endDraftId++;
        draftMap[endDraftId] = Draft({
            id: endDraftId,
            miners: miners,
            startHeight: startHeight
        });
        emit Propose(msg.sender, endDraftId, startHeight, miners);
    }

    function getDraftList() external view override returns (Draft[] memory) {
        uint count = endDraftId + 1 - startDraftId;
        Draft[] memory drafts = new Draft[](count);
        for (uint i = 0; i < count; i++) {
            drafts[i] = draftMap[startDraftId + i];
        }
        return drafts;
    }

    function vote(
        uint draftId,
        uint amount
    ) external payable override nonReentrant {
        require(
            draftId >= startDraftId && draftId <= endDraftId,
            "invalid draftId"
        );
        require(
            draftMap[draftId].startHeight > block.number,
            "invalid draft start height"
        );
        require(amount >= MIN_VOTE_AMOUNT, "insufficient amount");
        require(msg.value >= amount, "insufficient value");

        // add new record
        draftVoterMap[draftId][msg.sender] += amount;
        rewardShareMap[draftId][msg.sender] += amount;
        draftValueMap[draftId] += amount;
        emit Vote(msg.sender, draftId, amount);

        // check vote balance
        uint votedBalance = draftValueMap[draftId];
        if (votedBalance >= VOTE_TARGET_AMOUNT) {
            Draft memory draft = draftMap[draftId];
            Phase memory phase = Phase({
                startHeight: draft.startHeight,
                miners: draft.miners,
                preHeight: lastStartHeight,
                draftId: draftId,
                voteAmount: votedBalance
            });
            phaseMap[draft.startHeight] = phase;
            lastStartHeight = draft.startHeight;
            startDraftId = endDraftId + 1;

            // record current reward balance
            rewardRecord[draft.startHeight] =
                govReward.balance +
                totalWithdrawedReward;

            emit VotePass(
                votedBalance,
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

    function revokeVote(uint draftId) external override nonReentrant {
        require(draftId <= endDraftId, "invalid draftId");
        uint voteValue = draftVoterMap[draftId][msg.sender];
        require(voteValue > 0, "empty vote value");

        delete draftVoterMap[draftId][msg.sender];
        draftValueMap[draftId] -= voteValue;
        // only update rewardShareMap in current phase
        if (draftId >= startDraftId) {
            delete rewardShareMap[draftId][msg.sender];
        }
        safeTransferETH(msg.sender, voteValue);
        emit RevokeVote(msg.sender, draftId, voteValue);
    }

    function getRewardAmount(
        address addr
    ) public view override returns (uint reward) {
        Phase memory current = phaseMap[lastStartHeight];
        Phase memory prePhase = phaseMap[current.preHeight];
        uint currentReward = 0;
        uint preReward = 0;
        // the first phase, there is no reward
        while (current.startHeight > 1) {
            uint share = rewardShareMap[current.draftId][addr];
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
                current.voteAmount /
                RatioBase;

            // sum all the consensus reward
            for (uint i = 0; i < current.miners.length; i++) {
                if (addr == current.miners[i]) {
                    reward +=
                        (currentReward * (RatioBase - RatioVote)) /
                        current.miners.length /
                        RatioBase;
                    break;
                }
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

    function withdrawReward() external override nonReentrant {
        uint reward = getRewardAmount(msg.sender);
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
