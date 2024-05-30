// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

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
        needVote(keccak256("fundBridge"), keccak256(abi.encode(_amount)))
    {
        (bool success, ) = BRIDGE_PROXY.call{value: _amount}("");
        if (!success) {
            revert Errors.TransferFailed();
        }
        emit FundBridge(_amount);
    }
}
