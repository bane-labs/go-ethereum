// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {GovernanceVote} from "./base/GovernanceVote.sol";
import {GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";
import {Address} from "@openzeppelin/contracts/utils/Address.sol";

contract CommitteeMultiSig is GovernanceVote, GovProxyUpgradeable {
    // Execute an operation that calls a function on the `target` contract
    function execute(
        address target,
        bytes calldata data
    )
        external
        needVote(keccak256(abi.encode(target)), keccak256(abi.encode(data)))
        returns (bytes memory returndata)
    {
        returndata = Address.functionCall(target, data);
    }
}
