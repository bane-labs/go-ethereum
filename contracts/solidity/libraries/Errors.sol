// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

library Errors {
    // Universal Errors
    error NotAdmin();
    error NotGovernance();
    error TransferFailed();

    // GovernanceVote Errors
    error NotMiner();

    // Policy Errors
    error BlacklistExists();
    error BlacklistNotExists();
    error InvalidMinGasTipCap();
    error InvalidBaseFee();
    error InvalidCandidateLimit();

    // Governance Errors
    error SideCallNotAllowed();
    error OnlyEOA();
    error InsufficientValue();
    error InvalidShareRate();
    error RegisterDisabled();
    error SameCandidate();
    error CandidateExists();
    error CandidateNotExists();
    error LeftNotClaimed();
    error CandidateWithdrawNotAllowed();
    error MultipleVoteNotAllowed();
    error NoVote();
}
