// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../GovernanceVote.sol";

contract MockGovVote is GovernanceVote {
    uint public v;

    function changeV(
        uint newV
    )
        external
        needVote(
            keccak256(abi.encode("changeV")),
            keccak256(abi.encode(newV))
        )
    {
        v = newV;
    }
}
