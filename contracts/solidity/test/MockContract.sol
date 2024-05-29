// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IGovernance.sol";

contract MockContract {
    function call_registerCandidate(
        IGovernance governanceAddr,
        uint shareRate
    ) public payable {
        governanceAddr.registerCandidate{value: msg.value}(shareRate);
    }
}
