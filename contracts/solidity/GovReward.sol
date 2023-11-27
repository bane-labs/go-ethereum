// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

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
    struct Phase {
        uint startHeight;
        address[] miners;
        uint preHeight;
        uint draftId;
        uint voteAmount;
    }

    // get current consensus phase
    function getCurrentPhase() external view returns (Phase memory);
}

interface IGovReward {
    function getMiners() external view returns (address[] memory);

    function withdrawERC20(address to, address token, uint256 amount) external;

    function withdraw(address to, uint256 amount) external;
}

contract GovReward is IGovReward {
    // governance contact
    address public constant governance =
        0x1212000000000000000000000000000000000001;

    receive() external payable {}

    modifier onlyGov() {
        require(msg.sender == governance, "Not governance");
        _;
    }

    function getMiners() external view override returns (address[] memory) {
        return IGovernance(governance).getCurrentPhase().miners;
    }

    function withdrawERC20(
        address to,
        address token,
        uint256 amount
    ) external override onlyGov {
        TransferHelper.safeTransfer(token, to, amount);
    }

    function withdraw(address to, uint256 amount) external override onlyGov {
        TransferHelper.safeTransferETH(to, amount);
    }
}
