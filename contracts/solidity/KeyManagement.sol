// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {BLS12381} from "./libraries/BLS12381.sol";
import {IGovernance} from "./interfaces/IGovernance.sol";
import {IKeyManagement} from "./interfaces/IKeyManagement.sol";
import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";

contract KeyManagement is GovProxyUpgradeable, IKeyManagement {
    address public constant SELF = 0x1212100000000000000000000000000000000008;
    // governance contact
    address public constant GOV = 0x1212000000000000000000000000000000000001;
    address public constant SYS_CALL =
        0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE;
    // 0 - Recover, 1 - Share
    enum Period {
        Recover,
        Share
    }

    // public key for sharing message encryption
    mapping(address => string) public messagePubkeys;
    // height=>index=>shares
    mapping(uint => mapping(uint => bytes[])) public reshareMsgs;
    mapping(uint => mapping(uint => bytes[])) public shareMsgs;
    // height=>index=>index
    mapping(uint => mapping(uint => mapping(uint => bytes))) public recoverMsgs;
    // height=>index=>index
    mapping(uint => mapping(uint => bytes)) public rpvsses;
    mapping(uint => mapping(uint => bytes)) public spvsses;
    // bigA0 for verification and key generation
    mapping(uint => mapping(uint => bytes)) public sharedPubs;
    // global public keys
    // NOTE: this not the direct key for keystore encryption, should use pk = globalPub * scaler,
    // the scaler is used for speed up decryption, and not cool to be computed in contract.
    // ref https://github.com/bane-labs/go-ethereum/blob/a07310bd9a3a117ae0876ad69bbe8b6ed624aaa5/core/antimev/util.go#L27
    mapping(uint => bytes) public globalPubs;

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

    function registerMessageKey(
        address candidate,
        string calldata pubkey
    ) external {
        if (msg.sender != candidate && msg.sender != GOV)
            revert Errors.SideCallNotAllowed();
        if (tx.origin != candidate) revert Errors.OnlyEOA();
        messagePubkeys[msg.sender] = pubkey;
    }

    function reshare(bytes calldata pvss, bytes[] calldata messages) external {
        // check period
        (uint currentEpochHeight, uint targetHeight) = _checkPeriodAllowed(
            Period.Share
        );

        // check index
        uint index = indexOfResharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check input format
        (uint n, uint t) = numberAndThreshold();
        if (pvss.length != (t + n + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE)
            revert Errors.InvalidPVSS();
        if (messages.length != n) revert Errors.InvalidMessageAmount();

        // verify shared pub
        if (
            keccak256(sharedPubs[currentEpochHeight][index]) !=
            keccak256(pvss[:BLS12381.G1_SIZE])
        ) revert Errors.InvalidPVSS();

        // verify pvss
        _verifyPVSS(n, t, pvss);
        rpvsses[targetHeight][index] = pvss;

        // record messages
        reshareMsgs[targetHeight][index] = messages;

        // emit a event
        emit Reshare(targetHeight, index, msg.sender);
    }

    function share(bytes calldata pvss, bytes[] calldata messages) external {
        // check period
        (, uint targetHeight) = _checkPeriodAllowed(Period.Share);

        // check index
        uint index = indexOfSharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check input format
        (uint n, uint t) = numberAndThreshold();
        if (pvss.length != (t + n + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE)
            revert Errors.InvalidPVSS();
        if (messages.length != n) revert Errors.InvalidMessageAmount();

        // verify pvss
        _verifyPVSS(n, t, pvss);
        spvsses[targetHeight][index] = pvss;

        // record messages
        sharedPubs[targetHeight][index] = pvss[:BLS12381.G1_SIZE];
        shareMsgs[targetHeight][index] = messages;

        // emit a event
        emit Share(targetHeight, index, msg.sender);
    }

    function recover(uint[] calldata idxs, bytes[] calldata messages) external {
        // check period
        (, uint targetHeight) = _checkPeriodAllowed(Period.Recover);

        // check index
        uint index = indexOfResharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check absent secret index & record messages
        uint n = IGovernance(GOV).consensusSize();
        for (uint i = 0; i < idxs.length; i++) {
            if (idxs[i] > n || idxs[i] == 0) revert Errors.IndexOutOfRange();
            if (reshareMsgs[targetHeight][idxs[i]].length > 0)
                revert Errors.NoNeedForRecover();
            recoverMsgs[targetHeight][index][idxs[i]] = messages[i];
        }

        // emit a event
        emit Recover(targetHeight, index, msg.sender);
    }

    function reshareRecovered(
        bytes calldata pvss,
        bytes[] calldata messages
    ) external {
        // check period
        (uint currentEpochHeight, uint targetHeight) = _checkPeriodAllowed(
            Period.Recover
        );

        // check index
        uint index = indexOfSharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check input format
        (uint n, uint t) = numberAndThreshold();
        if (pvss.length != (t + n + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE)
            revert Errors.InvalidPVSS();
        if (messages.length != n) revert Errors.InvalidMessageAmount();

        // verify shared pub
        if (
            keccak256(sharedPubs[currentEpochHeight][index]) !=
            keccak256(pvss[:BLS12381.G1_SIZE])
        ) revert Errors.InvalidPVSS();

        // verify pvss
        _verifyPVSS(n, t, pvss);
        rpvsses[targetHeight][index] = pvss;

        // record messages
        if (reshareMsgs[targetHeight][index].length > 0)
            revert Errors.MessageExists();
        reshareMsgs[targetHeight][index] = messages;

        // emit a event
        emit Reshare(targetHeight, index, msg.sender);
    }

    // onPersistV2 is a persisting function that is called in the beginning of every
    // block by the system starting from NeoXAMEV fork.
    function onPersistV2() external {
        if (msg.sender != SYS_CALL) revert Errors.SideCallNotAllowed();
        // NOTE: should be called before Governance onPersist
        uint currentEpochHeight = IGovernance(GOV).currentEpochStartHeight();
        uint epochDuration = IGovernance(GOV).epochDuration();
        uint targetHeight = currentEpochHeight + epochDuration;
        // return if the new round key exists
        if (globalPubs[targetHeight].length > 0) return;

        // check reshare and share, compute global key
        uint n = IGovernance(GOV).consensusSize();
        if (reshareMsgs[targetHeight][1].length < n) return;
        if (shareMsgs[targetHeight][1].length < n) return;
        bytes memory output = sharedPubs[targetHeight][1];
        for (uint i = 2; i <= n; i++) {
            if (reshareMsgs[targetHeight][i].length < n) return;
            if (shareMsgs[targetHeight][i].length < n) return;
            output = BLS12381.g1Add(output, sharedPubs[targetHeight][i]);
        }

        // record global key
        // NOTE: this not the direct key for keystore encryption, should use pk = globalPub * scaler
        globalPubs[targetHeight] = output;
    }

    function isCurrentRoundReady() external view returns (bool) {
        uint currentEpochHeight = IGovernance(GOV).currentEpochStartHeight();
        return globalPubs[currentEpochHeight].length > 0;
    }

    function _verifyPVSS(uint n, uint t, bytes calldata pvss) internal view {
        _verifyR(t, pvss);
        _verifyBigf(n, t, pvss);
    }

    function _verifyR(uint t, bytes calldata pvss) internal view {
        // e(R1,G2)==e(G1,R2)
        bytes memory input = new bytes(
            2 * (BLS12381.G1_SIZE + BLS12381.G2_SIZE)
        );
        bytes memory g1One = BLS12381.g1One();
        bytes memory g2One = BLS12381.g2One();
        assembly {
            calldatacopy(add(input, 32), add(pvss.offset, mul(t, 128)), 128)
            mcopy(add(input, 160), add(g2One, 32), 256)
            mcopy(add(input, 416), add(g1One, 32), 128)
            calldatacopy(
                add(input, 544),
                add(add(pvss.offset, 128), mul(t, 128)),
                256
            )
        }
        if (!BLS12381.checkPairing(input)) revert Errors.InvalidPVSS();
    }

    function _verifyBigf(uint n, uint t, bytes calldata pvss) internal view {
        // F(i)==sum(A_{t-1}*i^(t-1))
        uint bigfiOffset = (t + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE;
        for (uint i = 0; i < n; i++) {
            bytes memory bigfi = pvss[(t - 1) * BLS12381.G1_SIZE:t *
                BLS12381.G1_SIZE];
            for (uint j = 1; j < t; j++) {
                bigfi = BLS12381.g1Mul(bigfi, i + 1);
                bigfi = BLS12381.g1Add(
                    bigfi,
                    pvss[(t - j - 1) * BLS12381.G1_SIZE:(t - j) *
                        BLS12381.G1_SIZE]
                );
            }
            if (
                keccak256(bigfi) !=
                keccak256(pvss[bigfiOffset:bigfiOffset + BLS12381.G1_SIZE])
            ) revert Errors.InvalidPVSS();
            bigfiOffset += BLS12381.G1_SIZE;
        }
    }

    function numberAndThreshold() public view returns (uint, uint) {
        uint n = IGovernance(GOV).consensusSize();
        return (n, n - (n - 1) / 3);
    }

    function indexOfSharing(address addr) public view returns (uint) {
        address[] memory pendingConsensus = IGovernance(GOV)
            .getPendingConsensus();
        for (uint i = 0; i < pendingConsensus.length; i++) {
            if (pendingConsensus[i] == addr) {
                return i + 1;
            }
        }
        return 0;
    }

    function indexOfResharing(address addr) public view returns (uint) {
        address[] memory currentConsensus = IGovernance(GOV)
            .getCurrentConsensus();
        for (uint i = 0; i < currentConsensus.length; i++) {
            if (currentConsensus[i] == addr) {
                return i + 1;
            }
        }
        return 0;
    }

    function indexCurrentNeedRecovering()
        external
        view
        returns (uint[] memory)
    {
        // check period
        (, uint targetHeight) = _checkPeriodAllowed(Period.Recover);

        uint[] memory idxs;
        uint n = IGovernance(GOV).consensusSize();
        for (uint i = 0; i < n; i++) {
            if (reshareMsgs[targetHeight][i + 1].length == 0) {
                assembly {
                    mstore(idxs, add(mload(idxs), 1))
                    mstore(add(idxs, mul(mload(idxs), 32)), add(i, 1))
                }
            }
        }
        return idxs;
    }

    function isShareReady() external view returns (bool) {
        // check period
        (, uint targetHeight) = _checkPeriodAllowed(Period.Recover);

        uint n = IGovernance(GOV).consensusSize();
        for (uint i = 1; i <= n; i++) {
            if (shareMsgs[targetHeight][i].length < n) {
                return false;
            }
        }
        return true;
    }

    function _checkPeriodAllowed(
        Period period
    ) internal view returns (uint, uint) {
        // check period
        uint currentEpochHeight = IGovernance(GOV).currentEpochStartHeight();
        uint epochDuration = IGovernance(GOV).epochDuration();
        uint periodDuration = IGovernance(GOV).sharePeriodDuration();
        uint targetHeight = currentEpochHeight + epochDuration;
        if (block.number < targetHeight - (uint(period) + 1) * periodDuration)
            revert Errors.PeriodNotStarted();
        if (block.number >= targetHeight - uint(period) * periodDuration)
            revert Errors.PeriodEnded();
        return (currentEpochHeight, targetHeight);
    }

    function getShareMsgs(
        uint height,
        uint index
    ) external view override returns (bytes[] memory) {
        return shareMsgs[height][index];
    }

    function getReshareMsgs(
        uint height,
        uint index
    ) external view override returns (bytes[] memory) {
        return reshareMsgs[height][index];
    }
}
