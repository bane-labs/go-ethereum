// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {IPolicy} from "./interfaces/IPolicy.sol";
import {GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";
import {PackedUserOperation} from "@openzeppelin/contracts/interfaces/IERC4337.sol";
import {ERC4337Utils} from "@openzeppelin/contracts/account/utils/ERC4337Utils.sol";
import {Paymaster} from "@openzeppelin/contracts/account/paymaster/Paymaster.sol";

contract GovPaymaster is Paymaster, GovProxyUpgradeable {
    // Policy contract
    address public constant POLICY = 0x1212000000000000000000000000000000000002;

    receive() external payable {
        // deposit received funds to the EntryPoint, but limit the balance to 0.1 GAS
        uint left = entryPoint().balanceOf(address(this));
        if (left < 0.4 ether) {
            uint limit = 0.4 ether - left;
            if (limit > address(this).balance) {
                _deposit(address(this).balance);
            } else {
                _deposit(limit);
            }
        }
    }

    function _validatePaymasterUserOp(
        PackedUserOperation calldata userOp,
        bytes32,
        uint256
    )
        internal
        view
        override
        returns (bytes memory context, uint256 validationData)
    {
        // ensure the blacklist policy
        if (IPolicy(POLICY).isBlackListed(userOp.sender)) {
            revert Errors.SenderBlacklisted();
        }
        // ensure the fee policy, and only sponsor the lowest gas tip for bundlers
        if (
            ERC4337Utils.maxPriorityFeePerGas(userOp) >
            (IPolicy(POLICY).minGasTipCap() * 12) / 10
        ) {
            revert Errors.GasTipTooHigh();
        }
        // we don't want the userOp to alloc and waste the space, which squeezes the traffic
        if (
            ERC4337Utils.maxFeePerGas(userOp) >
            IPolicy(POLICY).baseFee() +
                (IPolicy(POLICY).minGasTipCap() * 12) /
                10
        ) {
            revert Errors.MaxFeeTooHigh();
        }
        return ("", 0);
    }
}
