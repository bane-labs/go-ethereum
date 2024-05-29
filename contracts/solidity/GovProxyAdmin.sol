// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "./base/GovernanceVote.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

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
            bytes32(
                0xe739b9109d83c1c6d0d640fe9ed476fc5862a6de5483b00678a3fffa7a2be2f6
            ),
            keccak256(abi.encodePacked(proxy, newImplementation, data))
        )
    {
        proxy.upgradeToAndCall{value: msg.value}(newImplementation, data);
    }
}
