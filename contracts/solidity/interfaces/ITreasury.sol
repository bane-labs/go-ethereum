// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

interface ITreasury {
    event BridgeFund(uint256 amount);

    // release specific amount of GAS to bridge proxy address
    function fundBridge(uint256 _amount) external;
}
