// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IGovernance {
    event Activate(address candidate);
    event Deactivate(address candidate);
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
    function revokeVote(uint amount) external;

    // revoke votes, claim rewards and vote to another candidate
    function transferVote(address candidateTo) external;

    // only claim rewards
    function claimReward() external;

    // get reward amount to be claimed when settle
    function unclaimedRewardOf(address voter) external view returns (uint);

    // get pending group members
    function getPendingConsensus() external view returns (address[] memory);

    // get consensus group members
    function getCurrentConsensus() external view returns (address[] memory);

    // compute and update cached consensus group
    function onPersist() external;

    // compute and update cached consensus group, with dkg
    function onPersistV2() external;

    // activate a candidate in election
    function activateCandidate(address candidate) external;

    // deactivate a candidate in election
    function deactivateCandidate(address candidate) external;

    // get consensus size
    function consensusSize() external view returns (uint);

    // get epoch duration
    function epochDuration() external view returns (uint);

    // get share period duration
    function sharePeriodDuration() external view returns (uint);

    // get the start height of current epoch
    function currentEpochStartHeight() external view returns (uint);
}
