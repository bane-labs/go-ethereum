// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import "../libraries/Errors.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

abstract contract GovProxyUpgradeable is UUPSUpgradeable {
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;

    modifier onlyAdmin() {
        if (msg.sender != GOV_ADMIN) revert Errors.NotAdmin();
        _;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}
}
