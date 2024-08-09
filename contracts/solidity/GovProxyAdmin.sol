// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {GovernanceVote} from "./base/GovernanceVote.sol";
import {GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";
import {TimelockController} from "@openzeppelin/contracts/governance/TimelockController.sol";

/**
 * @dev This is an auxiliary contract meant to be assigned as the admin of a {Proxy}.
 * Use GovernanceVote to manage upgrade
 */
contract GovProxyAdmin is GovernanceVote, TimelockController {
    //bytes4(keccak256(bytes('upgradeToAndCall(address,bytes)')))
    bytes4 public constant UPGRADE_SELECTOR = 0x4f1ef286;

    /**
     * @dev This constructor does not affect the deployment code in the genesis file because we use GovProxyAdmin as a pre-deployment contract.
     * This constructor is only there because the inheritance of TimelockController requires a constructor to compile properly.
     */
    constructor(
        uint256 minDelay,
        address[] memory proposers,
        address[] memory executors,
        address admin
    ) TimelockController(minDelay, proposers, executors, admin) {}

    /**
     * @dev Schedule an operation that upgrades `proxy` to `newImplementation` and calls a function on the new implementation.
     *
     * Requirements:
     *
     * - need voting pass
     * - This contract must be the admin of `proxy`.
     */
    function scheduleUpgrade(
        GovProxyUpgradeable proxy,
        address newImplementation,
        bytes memory data
    )
        public
        payable
        virtual
        needVote(
            bytes32(
                // keccak256("upgradeAndCall")
                0xe739b9109d83c1c6d0d640fe9ed476fc5862a6de5483b00678a3fffa7a2be2f6
            ),
            keccak256(abi.encode(proxy, newImplementation, data))
        )
    {
        this.schedule(
            address(proxy),
            msg.value,
            abi.encodeWithSelector(UPGRADE_SELECTOR, newImplementation, data),
            0,
            0,
            getMinDelay()
        );
    }

    /**
     * @dev Execute an (ready) operation that upgrades `proxy` to `implementation` and calls a function on the new implementation.
     *
     * Requirements:
     *
     * - This contract must be the admin of `proxy`.
     */
    function executeUpgrade(
        GovProxyUpgradeable proxy,
        address newImplementation,
        bytes memory data
    ) public payable {
        this.execute(
            address(proxy),
            msg.value,
            abi.encodeWithSelector(UPGRADE_SELECTOR, newImplementation, data),
            0,
            0
        );
    }
}
