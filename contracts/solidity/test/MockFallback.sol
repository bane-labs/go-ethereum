// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract MockFallback {
    function call_fallback(address addr, bytes calldata data) public payable {
        bool success;
        assembly {
            calldatacopy(0, data.offset, calldatasize())
            success := call(
                gas(),
                addr,
                0,
                0,
                calldatasize(),
                0,
                returndatasize()
            )
            switch success
            case 0 {
                invalid()
            }
        }
    }
}
