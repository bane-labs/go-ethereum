// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

library Errors {
    // Universal Errors
    error OnlyEOA();
    error NotAdmin();
    error NotGovernance();
    error TransferFailed();

    // GovernanceVote Errors
    error NotMiner();

    // GovReward Errors
    error InvalidSelector();
    error InvalidGasLimit();
    error UnexpectedGasBurn();

    // Policy Errors
    error BlacklistExists();
    error BlacklistNotExists();
    error InvalidMinGasTipCap();
    error InvalidBaseFee();
    error InvalidCandidateLimit();
    error InvalidEnvelopeFee();
    error InvalidMaxEnvelopesPerBlock();
    error InvalidMaxEnvelopeGasLimit();

    // Governance Errors
    error SideCallNotAllowed();
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
    error ElectionLocked();

    // KeyManagement Errors
    error InvalidMessageKey();
    error InvalidRoundNumber();
    error NotShareMember();
    error PeriodNotStarted();
    error PeriodEnded();
    error NoNeedForRecover();
    error InvalidBLS12381Input();
    error InvalidPVSS();
    error InvalidMessageAmount();
    error IndexOutOfRange();
    error MessageExists();
}
