// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract MockMultiSig {
    uint public v;

    function changeV(uint newV) external {
        v = newV;
    }
}
