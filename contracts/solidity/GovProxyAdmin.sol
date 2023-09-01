// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "./lib/TransparentUpgradeableProxy.sol";
import "./GovernanceVote.sol";

/**
 * @dev This is an auxiliary contract meant to be assigned as the admin of a {TransparentUpgradeableProxy}. For an
 * explanation of why you would want to use this see the documentation for {TransparentUpgradeableProxy}.
 * Use GovernanceVote to manage upgrade
 */
contract GovProxyAdmin is GovernanceVote {
    /**
     * @dev Returns the current implementation of `proxy`.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function getProxyImplementation(
        ITransparentUpgradeableProxy proxy
    ) public view virtual returns (address) {
        // We need to manually run the static call since the getter cannot be flagged as view
        // bytes4(keccak256("implementation()")) == 0x5c60da1b
        (bool success, bytes memory returndata) = address(proxy).staticcall(
            hex"5c60da1b"
        );
        require(success);
        return abi.decode(returndata, (address));
    }

    /**
     * @dev Returns the current admin of `proxy`.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function getProxyAdmin(
        ITransparentUpgradeableProxy proxy
    ) public view virtual returns (address) {
        // We need to manually run the static call since the getter cannot be flagged as view
        // bytes4(keccak256("admin()")) == 0xf851a440
        (bool success, bytes memory returndata) = address(proxy).staticcall(
            hex"f851a440"
        );
        require(success);
        return abi.decode(returndata, (address));
    }

    /**
     * @dev Changes the admin of `proxy` to `newAdmin`.
     *
     * Requirements:
     *
     * - This contract must be the current admin of `proxy`.
     */
    function changeProxyAdmin(
        ITransparentUpgradeableProxy proxy,
        address newAdmin
    )
        public
        virtual
        needVote(
            keccak256("changeProxyAdmin"),
            keccak256(abi.encode(proxy, newAdmin))
        )
    {
        proxy.changeAdmin(newAdmin);
    }

    /**
     * @dev Upgrades `proxy` to `implementation`. See {TransparentUpgradeableProxy-upgradeTo}.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function upgrade(
        ITransparentUpgradeableProxy proxy,
        address implementation
    )
        public
        virtual
        needVote(
            keccak256("upgrade"),
            keccak256(abi.encode(proxy, implementation))
        )
    {
        proxy.upgradeTo(implementation);
    }

    /**
     * @dev Upgrades `proxy` to `implementation` and calls a function on the new implementation. See
     * {TransparentUpgradeableProxy-upgradeToAndCall}.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function upgradeAndCall(
        ITransparentUpgradeableProxy proxy,
        address implementation,
        bytes memory data
    )
        public
        payable
        virtual
        needVote(
            keccak256("upgradeAndCall"),
            keccak256(abi.encode(proxy, implementation, data))
        )
    {
        proxy.upgradeToAndCall{value: msg.value}(implementation, data);
    }
}
