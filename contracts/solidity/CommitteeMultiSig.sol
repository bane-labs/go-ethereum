// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {GovernanceVote} from "./base/GovernanceVote.sol";
import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";
import {Address} from "@openzeppelin/contracts/utils/Address.sol";

contract CommitteeMultiSig is GovernanceVote, GovProxyUpgradeable {
    address public constant SELF = 0x1212100000000000000000000000000000000007;

    // Only for precompiled uups implementation in genesis file, need to be removed when upgrading the contract.
    // This override is added because "immutable __self" in UUPSUpgradeable is not avaliable in precompiled contract.
    function _checkProxy() internal view virtual override {
        if (
            address(this) == SELF || // Must be called through delegatecall
            ERC1967Utils.getImplementation() != SELF // Must be called through an active proxy
        ) {
            revert UUPSUnauthorizedCallContext();
        }
    }

    // Only for precompiled uups implementation in genesis file, need to be removed when upgrading the contract.
    // This override is added because "immutable __self" in UUPSUpgradeable is not avaliable in precompiled contract.
    function _checkNotDelegated() internal view virtual override {
        if (address(this) != SELF) {
            // Must not be called through delegatecall
            revert UUPSUnauthorizedCallContext();
        }
    }

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
