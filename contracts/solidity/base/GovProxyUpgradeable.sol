// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "../libraries/Errors.sol";
import {ERC1967Utils, UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

abstract contract GovProxyUpgradeable is UUPSUpgradeable {
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;

    modifier onlyAdmin() {
        if (msg.sender != GOV_ADMIN) revert Errors.NotAdmin();
        _;
    }

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}
}
