// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IPolicy {
    event AddBlackList(address indexed addr);
    event RemoveBlackList(address indexed addr);
    event SetMinGasTipCap(uint256 gasTipCap);
    event SetBaseFee(uint256 baseFee);
    event SetCandidateLimit(uint256 candidateLimit);

    // add an address to blacklist policy
    function addBlackList(address _addr) external;

    // remove an address from blacklist policy
    function removeBlackList(address _addr) external;

    // set a new value to minimum gas tip cap policy
    function setMinGasTipCap(uint256 _gasTipCap) external;

    // set a new value to base fee policy
    function setBaseFee(uint256 _baseFee) external;

    // return the value of candidate limit policy
    function candidateLimit() external view returns (uint256);
}
