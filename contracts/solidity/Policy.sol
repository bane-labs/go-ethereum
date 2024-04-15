// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.20;

import "./GovernanceVote.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract Policy is GovernanceVote, UUPSUpgradeable {
    address public constant SELF = 0x1212100000000000000000000000000000000002;
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;

    mapping(address => bool) public isBlackListed;
    uint256 public minGasTipCap;
    uint256 public baseFee;

    event AddBlackList(address addr);
    event RemoveBlackList(address addr);
    event SetMinGasTipCap(uint256 gasTipCap);
    event SetBaseFee(uint256 baseFee);

    modifier onlyAdmin() {
        require(msg.sender == GOV_ADMIN, "not admin");
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
        needVote(keccak256("addBlackList"), keccak256(abi.encode(_addr)))
    {
        require(!isBlackListed[_addr], "Policy: Blacklist already exists");
        isBlackListed[_addr] = true;
        emit AddBlackList(_addr);
    }

    // remove blacklist
    function removeBlackList(
        address _addr
    )
        external
        needVote(keccak256("removeBlackList"), keccak256(abi.encode(_addr)))
    {
        require(isBlackListed[_addr], "Policy: Blacklist does not exist");
        delete isBlackListed[_addr];
        emit RemoveBlackList(_addr);
    }

    // set minimum gas tip cap
    function setMinGasTipCap(
        uint256 _gasTipCap
    )
        external
        needVote(
            keccak256("setMinGasTipCap"),
            keccak256(abi.encode(_gasTipCap))
        )
    {
        require(_gasTipCap > 0, "Policy: setMinGasTipCap invalid parameter");
        minGasTipCap = _gasTipCap;
        emit SetMinGasTipCap(_gasTipCap);
    }

    // set base fee
    function setBaseFee(
        uint256 _baseFee
    )
        external
        needVote(keccak256("setBaseFee"), keccak256(abi.encode(_baseFee)))
    {
        require(_baseFee > 0, "Policy: setBaseFee invalid parameter");
        baseFee = _baseFee;
        emit SetBaseFee(_baseFee);
    }
}
