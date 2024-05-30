// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "./GovernanceVote.sol";

/**
 * @dev This is an auxiliary contract meant to be assigned as the Neo X treasury for funding the native bridge proxy.
 * Use GovernanceVote to manage funding the bridge.
 */
contract Treasury is GovernanceVote {
    address public constant BRIDGE_PROXY =
        0x1212000000000000000000000000000000000004;

    event FundBridge(uint256 amount);

    error FundingFailed();

    /**
     * @dev Sends `amount` of ether to the bridge proxy.
     */
    function fundBridge(
        uint256 _amount
    )
        external
        needVote(
            bytes32(
                0xdd6d322687f552c30b168d744bbd29145a2095a3557a58387f7e7230c9449179
            ),
            keccak256(abi.encode(_amount))
        )
    {
        (bool success, ) = BRIDGE_PROXY.call{value: _amount}("");
        if (!success) {
            revert Errors.TransferFailed();
        }
        emit FundBridge(_amount);
    }
}
