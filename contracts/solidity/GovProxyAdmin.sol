// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "./GovernanceVote.sol";

/**
 * @dev This is an auxiliary contract meant to be assigned as the admin of a {Proxy}.
 * Use GovernanceVote to manage upgrade
 */
contract GovProxyAdmin is GovernanceVote {
    /**
     * @dev Upgrades the implementation in proxy to `newImplementation`, and
     * subsequently executes the function call encoded in `data`. See
     * {UUPSUpgradeable-upgradeToAndCall}.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function upgradeAndCall(
        UUPSUpgradeable proxy,
        address newImplementation,
        bytes memory data
    )
        public
        payable
        virtual
        needVote(
            keccak256("upgradeAndCall"),
            keccak256(abi.encode(proxy, newImplementation, data))
        )
    {
        proxy.upgradeToAndCall{value: msg.value}(newImplementation, data);
    }
}
