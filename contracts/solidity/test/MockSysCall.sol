// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IGovernance {
    function onPersist() external;
}

contract MockSysCall {
    function call_onPersist(IGovernance governanceAddr) public {
        governanceAddr.onPersist();
    }
}
