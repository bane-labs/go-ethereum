// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {GovProxyUpgradeable} from "../base/GovProxyUpgradeable.sol";

contract MockGovProxyUpgradeable is GovProxyUpgradeable {
    function initialize() external initializer {}

    function reinitialize() external reinitializer(1) {}
}
