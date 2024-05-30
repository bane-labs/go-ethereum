// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.25;

import "./Errors.sol";
import "./GovernanceVote.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract Policy is GovernanceVote, UUPSUpgradeable {
    address public constant SELF = 0x1212100000000000000000000000000000000002;
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;

    mapping(address => bool) public isBlackListed;
    uint256 public minGasTipCap;
    uint256 public baseFee;

    event AddBlackList(address indexed addr);
    event RemoveBlackList(address indexed addr);
    event SetMinGasTipCap(uint256 gasTipCap);
    event SetBaseFee(uint256 baseFee);

    modifier onlyAdmin() {
        if (msg.sender != GOV_ADMIN) revert Errors.NotAdmin();
        _;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}

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

    // add blacklist
    function addBlackList(
        address _addr
    )
        external
        needVote(
            bytes32(
                0x4912b57f7ea75243ecaff76a75bdedbc13a6f58c1c967b0427b8aee0a276309e
            ),
            keccak256(abi.encodePacked(_addr))
        )
    {
        if (isBlackListed[_addr]) revert Errors.BlacklistExists();
        isBlackListed[_addr] = true;
        emit AddBlackList(_addr);
    }

    // remove blacklist
    function removeBlackList(
        address _addr
    )
        external
        needVote(
            bytes32(
                0x310cc9bfce6443143f03d0cdc4d66afa0b3c689539eb3e65cb1820b56d672465
            ),
            keccak256(abi.encodePacked(_addr))
        )
    {
        if (!isBlackListed[_addr]) revert Errors.BlacklistNotExists();
        delete isBlackListed[_addr];
        emit RemoveBlackList(_addr);
    }

    // set minimum gas tip cap
    function setMinGasTipCap(
        uint256 _gasTipCap
    )
        external
        needVote(
            bytes32(
                0x089197e4f35b8ada456b5531e8c1759ee3fce703602a3a957b5c9d2831082156
            ),
            keccak256(abi.encodePacked(_gasTipCap))
        )
    {
        if (_gasTipCap <= 0) revert Errors.InvalidMinGasTipCap();
        minGasTipCap = _gasTipCap;
        emit SetMinGasTipCap(_gasTipCap);
    }

    // set base fee
    function setBaseFee(
        uint256 _baseFee
    )
        external
        needVote(
            bytes32(
                0x83113031fe9312a872d9176bc1a087dc38ca109c517a596998332e2fb8409acc
            ),
            keccak256(abi.encodePacked(_baseFee))
        )
    {
        if (_baseFee <= 0) revert Errors.InvalidBaseFee();
        baseFee = _baseFee;
        emit SetBaseFee(_baseFee);
    }
}
