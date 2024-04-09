// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "./GovernanceVote.sol";

/**
 * @dev This is an auxiliary contract meant to be assigned as the admin of a {Proxy}.
 * Use GovernanceVote to manage upgrade
 */
contract GovProxyAdmin is GovernanceVote {
    /**
     * @dev Upgrades `proxy` to `implementation` and calls a function on the new implementation. See
     * {TransparentUpgradeableProxy-upgradeToAndCall}.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function upgradeAndCall(
        UUPSUpgradeable proxy,
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
