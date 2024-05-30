// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract MockBridge {
    address public funder;

    modifier onlyFunder() {
        if (msg.sender != funder) revert();
        _;
    }

    receive() external payable onlyFunder {}
}
