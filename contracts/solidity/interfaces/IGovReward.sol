// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface IGovReward {
    // withdraw all miner rewards to governance contract
    function withdraw() external;

    // get and return current consensus group as miners
    function getMiners() external view returns (address[] memory);
}
