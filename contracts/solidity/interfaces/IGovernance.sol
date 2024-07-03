// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

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

    // get consensus size
    function consensusSize() external view returns (uint);
}
