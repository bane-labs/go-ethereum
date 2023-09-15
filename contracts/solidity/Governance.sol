// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";

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
    }

    event Propose(
        address proposer,
        uint draftId,
        uint startHeight,
        address[] miners
    );

    event Vote(address voter, uint draftId);

    event VotePass(
        uint votedBalance,
        uint startHeight,
        address[] miners,
        uint preHeight
    );

    // propose draft, contains start height and consensus list
    function propose(uint startHeight, address[] memory miners) external;

    // get Draft list, contians unique id
    function getDraftList() external view returns (Draft[] memory);

    // vote draft with gas
    // when the vote condition meets: 1. convert draft to phase; 2.clean draft list
    function vote(uint256 draftId) external;

    // revoke vote
    function revokeVote() external;

    // get current consensus phase
    function getCurrentPhase() external view returns (Phase memory);

    // get consensus phase by start height
    function getPhaseByHeight(uint height) external view returns (Phase memory);
}

contract Governance is IGovernance {
    using EnumerableSet for EnumerableSet.AddressSet;

    // the min balance for voting
    uint public constant MIN_VOTE_AMOUNT = 10 ether;
    // the balance target for a vote to pass
    uint public constant VOTE_TARGET_AMOUNT = 3000000 ether;

    // the last Phase's start height, default 1
    uint public lastStartHeight;
    // draft list start id, default 1
    uint public startDraftId;
    // draft list end id, default 0
    uint public endDraftId;
    // Phase mapping to store Phase, key is the start height of Phase,
    // should add first phase in genesis block
    mapping(uint => Phase) private phaseMap;
    // vote mapping, user address -> draftId
    mapping(address => uint) private draftVoteMap;
    // vote list mapping,  draftId -> user address list
    mapping(uint => EnumerableSet.AddressSet) private draftVoteList;
    // Draft mapping, draftId -> Draft
    mapping(uint => Draft) private draftMap;

    // constructor should not be in upgradable contract, this method is just for test
    // governance contract should predeploy in genesis.json
    constructor() {
        lastStartHeight = 1;
        startDraftId = 1;
        address[] memory defaultMiners = new address[](1);
        defaultMiners[0] = msg.sender;
        phaseMap[1] = Phase({
            startHeight: 1,
            preHeight: 0,
            miners: defaultMiners
        });
    }

    receive() external payable {}

    modifier onlyConsensus() {
        require(isMiner(msg.sender), "Not Consensus");
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
            "propose should after last phase active"
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
        uint count = endDraftId - startDraftId + 1;
        Draft[] memory drafts = new Draft[](count);
        for (uint i = 0; i < count; i++) {
            drafts[i] = draftMap[startDraftId + i];
        }
        return drafts;
    }

    function vote(uint256 draftId) external override {
        require(
            draftId >= startDraftId && draftId <= endDraftId,
            "invalid draftId"
        );

        uint balance = msg.sender.balance;
        require(balance >= MIN_VOTE_AMOUNT, "not enough balance");

        uint votedId = draftVoteMap[msg.sender];
        if (votedId == draftId) {
            return;
        }
        if (votedId > 0) // remove voted record
        {
            draftVoteList[votedId].remove(msg.sender);
        }

        // set new record
        draftVoteMap[msg.sender] = draftId;
        draftVoteList[draftId].add(msg.sender);
        emit Vote(msg.sender, draftId);

        // check vote balance
        uint votedBalance = getVoteBalance(draftId);
        if (votedBalance >= VOTE_TARGET_AMOUNT) {
            Draft memory draft = draftMap[draftId];
            Phase memory phase = Phase({
                startHeight: draft.startHeight,
                miners: draft.miners,
                preHeight: lastStartHeight
            });
            phaseMap[draft.startHeight] = phase;
            lastStartHeight = draft.startHeight;
            startDraftId = endDraftId + 1;
            emit VotePass(
                votedBalance,
                phase.startHeight,
                phase.miners,
                phase.preHeight
            );
        }
    }

    function revokeVote() external override {
        uint draftId = draftVoteMap[msg.sender];
        require(
            draftId >= startDraftId && draftId <= endDraftId,
            "invalid draftId"
        );

        draftVoteList[draftId].remove(msg.sender);
        delete draftVoteMap[msg.sender];
        emit Vote(msg.sender, 0);
    }

    // calculate vote balance of draft
    function getVoteBalance(uint draftId) internal view returns (uint balance) {
        uint length = draftVoteList[draftId].length();
        for (uint i = 0; i < length; i++) {
            balance += draftVoteList[draftId].at(i).balance;
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
