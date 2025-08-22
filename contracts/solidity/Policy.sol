// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {IGovernance} from "./interfaces/IGovernance.sol";
import {IPolicy} from "./interfaces/IPolicy.sol";
import {GovernanceVote} from "./base/GovernanceVote.sol";
import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";

contract Policy is IPolicy, GovernanceVote, GovProxyUpgradeable {
    address public constant SELF = 0x1212100000000000000000000000000000000002;
    // governance contact
    address public constant GOV = 0x1212000000000000000000000000000000000001;
    uint256 public constant DEFAULT_CANDIDATE_LIMIT = 2000;

    mapping(address => bool) public isBlackListed;
    uint256 public minGasTipCap;
    uint256 public baseFee;
    uint256 internal candidateLimit;
    uint256 public envelopeFee;
    uint256 public maxEnvelopesPerBlock;
    uint256 public maxEnvelopeGasLimit;

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

    function addBlackList(
        address _addr
    )
        external
        needVote(
            bytes32(
                // keccak256("addBlackList")
                0x4912b57f7ea75243ecaff76a75bdedbc13a6f58c1c967b0427b8aee0a276309e
            ),
            keccak256(abi.encode(_addr))
        )
    {
        if (isBlackListed[_addr]) revert Errors.BlacklistExists();
        isBlackListed[_addr] = true;
        IGovernance(GOV).deactivateCandidate(_addr);
        emit AddBlackList(_addr);
    }

    function removeBlackList(
        address _addr
    )
        external
        needVote(
            bytes32(
                // keccak256("removeBlackList")
                0x310cc9bfce6443143f03d0cdc4d66afa0b3c689539eb3e65cb1820b56d672465
            ),
            keccak256(abi.encode(_addr))
        )
    {
        if (!isBlackListed[_addr]) revert Errors.BlacklistNotExists();
        delete isBlackListed[_addr];
        IGovernance(GOV).activateCandidate(_addr);
        emit RemoveBlackList(_addr);
    }

    function setMinGasTipCap(
        uint256 _gasTipCap
    )
        external
        needVote(
            bytes32(
                // keccak256("setMinGasTipCap")
                0x089197e4f35b8ada456b5531e8c1759ee3fce703602a3a957b5c9d2831082156
            ),
            keccak256(abi.encode(_gasTipCap))
        )
    {
        if (_gasTipCap <= 0) revert Errors.InvalidMinGasTipCap();
        minGasTipCap = _gasTipCap;
        emit SetMinGasTipCap(_gasTipCap);
    }

    function setBaseFee(
        uint256 _baseFee
    )
        external
        needVote(
            bytes32(
                // keccak256("setBaseFee")
                0x83113031fe9312a872d9176bc1a087dc38ca109c517a596998332e2fb8409acc
            ),
            keccak256(abi.encode(_baseFee))
        )
    {
        if (_baseFee <= 0) revert Errors.InvalidBaseFee();
        baseFee = _baseFee;
        emit SetBaseFee(_baseFee);
    }

    function setCandidateLimit(
        uint256 _candidateLimit
    )
        external
        needVote(
            bytes32(
                // keccak256("setCandidateLimit")
                0x172d358b638a8ee3e962dd73800c4025c48eb0f79c479bc2cdd1f63e72779efc
            ),
            keccak256(abi.encode(_candidateLimit))
        )
    {
        if (_candidateLimit < IGovernance(GOV).consensusSize()) revert Errors.InvalidCandidateLimit();
        candidateLimit = _candidateLimit;
        emit SetCandidateLimit(_candidateLimit);
    }

    function getCandidateLimit() external view returns (uint256) {
        uint256 limit = candidateLimit;
        if (limit > 0) return limit;
        else return DEFAULT_CANDIDATE_LIMIT;
    }

    function setEnvelopeFee(
        uint256 _fee
    )
        external
        needVote(
            bytes32(
                // keccak256("setEnvelopeFee")
                0xab26bca1cb5d7b0a97ee434cabffdf1efbc3073c1645a8ed4d1732335c51df49
            ),
            keccak256(abi.encode(_fee))
        )
    {
        if (_fee <= 0) revert Errors.InvalidEnvelopeFee();
        envelopeFee = _fee;
        emit SetEnvelopeFee(_fee);
    }

    function setMaxEnvelopesPerBlock(
        uint256 _number
    )
        external
        needVote(
            bytes32(
                // keccak256("setMaxEnvelopesPerBlock")
                0x468ff90b65b7330769fd5fa1950650ac15948bbbaaff84f86b3c11a5dc7842d1
            ),
            keccak256(abi.encode(_number))
        )
    {
        if (_number <= 0) revert Errors.InvalidMaxEnvelopesPerBlock();
        maxEnvelopesPerBlock = _number;
        emit SetMaxEnvelopesPerBlock(_number);
    }

    function setMaxEnvelopeGasLimit(
        uint256 _gaslimit
    )
        external
        needVote(
            bytes32(
                // keccak256("setMaxEnvelopeGasLimit")
                0xf48e9af07262cd1c77a4209ac59848c8bf3f8ec29817678aa7f1b67dd990501c
            ),
            keccak256(abi.encode(_gaslimit))
        )
    {
        if (_gaslimit < 21000) revert Errors.InvalidMaxEnvelopeGasLimit();
        maxEnvelopeGasLimit = _gaslimit;
        emit SetMaxEnvelopeGasLimit(_gaslimit);
    }
}
