// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";

/**
 * @dev This is a system contract stub to be replaced by the DKG contract once
 * it's properly implemented.
 */
contract DKG is GovProxyUpgradeable {
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
}
