// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {Bytes} from "./libraries/Bytes.sol";
import {IGovReward} from "./interfaces/IGovReward.sol";
import {IGovernance} from "./interfaces/IGovernance.sol";
import {GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";

contract GovReward is IGovReward, GovProxyUpgradeable {
    // governance contact
    address public constant GOV = 0x1212000000000000000000000000000000000001;

    receive() external payable {}

    fallback() external payable {
        if (msg.sig != bytes4(0xffffffff)) {
            revert Errors.InvalidSelector();
        }
        // burn required gas amount internally
        uint32 requiredSpace = Bytes.decodeUint32(msg.data[8:12]);
        if (requiredSpace < 21000) {
            revert Errors.InvalidGasLimit();
        }
        (bool success, bytes memory data) = address(this).staticcall{
            gas: requiredSpace - 21000
        }(abi.encodeWithSelector(this._wasteGas.selector));
        if (success || data.length > 0) {
            revert Errors.UnexpectedGasBurn();
        }
    }

    modifier onlyGov() {
        if (msg.sender != GOV) revert Errors.NotGovernance();
        _;
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

    function _wasteGas() external pure {
        while (true) {}
    }
}
