// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

library Bytes {
    function toBytes4(
        bytes memory data
    ) internal pure returns (bytes4) {
        bytes4 out;
        for (uint256 i = 0; i < 4; i++) {
            out |= bytes4(data[i] & 0xFF) >> (i * 8);
        }
        return out;
    }

    function decodeUint32(bytes memory data) internal pure returns (uint32) {
        require(data.length == 4, "Bad data");
        return uint32(toBytes4(data));
    }
}
