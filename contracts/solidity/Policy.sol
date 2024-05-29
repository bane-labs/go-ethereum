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
    uint256 public candidateLimit;

    event AddBlackList(address indexed addr);
    event RemoveBlackList(address indexed addr);
    event SetMinGasTipCap(uint256 gasTipCap);
    event SetBaseFee(uint256 baseFee);
    event SetCandidateLimit(uint256 candidateLimit);

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
        needVote(keccak256("addBlackList"), keccak256(abi.encode(_addr)))
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
        needVote(keccak256("removeBlackList"), keccak256(abi.encode(_addr)))
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
            keccak256("setMinGasTipCap"),
            keccak256(abi.encode(_gasTipCap))
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
        needVote(keccak256("setBaseFee"), keccak256(abi.encode(_baseFee)))
    {
        if (_baseFee <= 0) revert Errors.InvalidBaseFee();
        baseFee = _baseFee;
        emit SetBaseFee(_baseFee);
    }

    // set candidate limit (increase only)
    function setCandidateLimit(
        uint256 _candidateLimit
    )
        external
        needVote(
            bytes32(
                0x172d358b638a8ee3e962dd73800c4025c48eb0f79c479bc2cdd1f63e72779efc
            ),
            keccak256(abi.encodePacked(_candidateLimit))
        )
    {
        if (_candidateLimit <= candidateLimit)
            revert Errors.InvalidCandidateLimit();
        candidateLimit = _candidateLimit;
        emit SetCandidateLimit(_candidateLimit);
    }
}
