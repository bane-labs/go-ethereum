// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "./interfaces/IGovReward.sol";
import "./interfaces/IGovernance.sol";
import "./base/GovProxyUpgradeable.sol";

contract GovReward is IGovReward, GovProxyUpgradeable {
    address public constant SELF = 0x1212100000000000000000000000000000000003;
    // governance contact
    address public constant GOV = 0x1212000000000000000000000000000000000001;

    receive() external payable {}

    modifier onlyGov() {
        if (msg.sender != GOV) revert Errors.NotGovernance();
        _;
    }

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

    function getMiners() external view override returns (address[] memory) {
        return IGovernance(GOV).getCurrentConsensus();
    }

    function withdraw() external onlyGov {
        if (address(this).balance > 0) {
            _safeTransferETH(GOV, address(this).balance);
        }
    }

    function _safeTransferETH(address to, uint value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        if (!success) revert Errors.TransferFailed();
    }
}
