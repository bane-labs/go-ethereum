// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IPolicy {
    event AddBlackList(address indexed addr);
    event RemoveBlackList(address indexed addr);
    event SetMinGasTipCap(uint256 gasTipCap);
    event SetBaseFee(uint256 baseFee);
    event SetCandidateLimit(uint256 candidateLimit);
    event SetEnvelopeFee(uint256 envelopeFee);
    event SetMaxEnvelopesPerBlock(uint256 maxEnvelopesPerBlock);
    event SetMaxEnvelopeGasLimit(uint256 setMaxEnvelopeGasLimit);

    // add an address to blacklist policy
    function addBlackList(address _addr) external;

    // remove an address from blacklist policy
    function removeBlackList(address _addr) external;

    // check if an address is blacklisted by policy
    function isBlackListed(address _addr) external view returns (bool);

    // set a new value to minimum gas tip cap policy
    function setMinGasTipCap(uint256 _gasTipCap) external;

    // get the value of minimum gas tip cap policy
    function minGasTipCap() external view returns (uint256);

    // set a new value to base fee policy
    function setBaseFee(uint256 _baseFee) external;

    // get the value of base fee policy
    function baseFee() external view returns (uint256);

    // set candidate limit (increase only)
    function setCandidateLimit(uint256 _candidateLimit) external;

    // return the value of candidate limit policy
    function getCandidateLimit() external view returns (uint256);

    // set the maximum number of envelope transactions to be packed in each block
    function setMaxEnvelopesPerBlock(uint256 _number) external;

    // get the value of envelope transaction number policy
    function maxEnvelopesPerBlock() external view returns (uint256);

    // set the maximum value of envelope transaction gas limit
    function setMaxEnvelopeGasLimit(uint256 _gaslimit) external;

    // get the value of envelope transaction gas limit policy
    function maxEnvelopeGasLimit() external view returns (uint256);
}
