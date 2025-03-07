// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {IGovernance} from "../interfaces/IGovernance.sol";

contract MockContract {
    function call_registerCandidate(
        IGovernance governanceAddr,
        uint shareRate,
        bytes calldata pubkey
    ) public payable {
        governanceAddr.registerCandidate{value: msg.value}(shareRate, pubkey);
    }
}
