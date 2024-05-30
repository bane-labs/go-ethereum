// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {IGovernance} from "../interfaces/IGovernance.sol";

contract MockSysCall {
    function call_onPersist(IGovernance governanceAddr) public {
        governanceAddr.onPersist();
    }
}
