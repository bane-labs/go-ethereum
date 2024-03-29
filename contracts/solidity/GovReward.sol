// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

library TransferHelper {
    function safeTransfer(address token, address to, uint256 value) internal {
        // bytes4(keccak256(bytes('transfer(address,uint256)')));
        (bool success, bytes memory data) = token.call(
            abi.encodeWithSelector(0xa9059cbb, to, value)
        );
        require(
            success && (data.length == 0 || abi.decode(data, (bool))),
            "safeTransfer: transfer failed"
        );
    }

    function safeTransferETH(address to, uint256 value) internal {
        (bool success, ) = to.call{value: value}(new bytes(0));
        require(success, "safeTransferETH: ETH transfer failed");
    }
}

interface IGovernance {
    // get current consensus group
    function getCurrentConsensus() external view returns (address[] memory);
}

interface IGovReward {
    function getMiners() external view returns (address[] memory);

    function withdraw() external;
}

contract GovReward is IGovReward, UUPSUpgradeable {
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;
    // governance contact
    address public constant governance =
        0x1212000000000000000000000000000000000001;

    receive() external payable {}

    modifier onlyGov() {
        require(msg.sender == governance, "Not governance");
        _;
    }

    modifier onlyAdmin() {
        require(msg.sender == GOV_ADMIN, "Not admin");
        _;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}

    function getMiners() external view override returns (address[] memory) {
        return IGovernance(governance).getCurrentConsensus();
    }

    function withdraw() external onlyGov {
        if (address(this).balance > 0) {
            TransferHelper.safeTransferETH(governance, address(this).balance);
        }
    }
}
