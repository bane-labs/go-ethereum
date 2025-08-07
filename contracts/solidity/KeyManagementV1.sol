// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

import {Errors} from "./libraries/Errors.sol";
import {BLS12381} from "./libraries/BLS12381.sol";
import {OneMessageVerifier} from "./libraries/OneMessageVerifier.sol";
import {TwoMessageVerifier} from "./libraries/TwoMessageVerifier.sol";
import {SevenMessageVerifier} from "./libraries/SevenMessageVerifier.sol";
import {IGovernance} from "./interfaces/IGovernance.sol";
import {IKeyManagement} from "./interfaces/IKeyManagement.sol";
import {IZKDKGV1} from "./interfaces/IZKDKGV1.sol";
import {ERC1967Utils, GovProxyUpgradeable} from "./base/GovProxyUpgradeable.sol";

contract KeyManagementV1 is GovProxyUpgradeable, IKeyManagement, IZKDKGV1 {
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
    uint public constant ZK_VERSION = 1;

    // succeeded dkg round index, starts from 1, 0 means empty
    uint public roundNumber;
    // height=>round
    mapping(uint => uint) public roundNumberOfEpochs;
    // public key for sharing message encryption
    mapping(address => bytes) public messagePubkeys;
    // round=>index=>shares, the index of msg is the array index, starts from 0
    mapping(uint => mapping(uint => bytes[])) public reshareMsgs;
    mapping(uint => mapping(uint => bytes[])) public shareMsgs;
    // round=>index=>index, the index of msg is the array index, starts from 0
    mapping(uint => mapping(uint => mapping(uint => bytes))) public recoverMsgs;
    // round=>index=>pvss
    mapping(uint => mapping(uint => bytes)) public rpvsses;
    mapping(uint => mapping(uint => bytes)) public spvsses;
    // round for verification and key generation
    mapping(uint => mapping(uint => bytes)) public sharedPubs;
    // aggregated commitments from pvss
    // NOTE: this not the direct key for keystore encryption, should use pk = aggregatedCommitment * scaler,
    // the scaler is used for speed up decryption, and not cool to be computed in contract.
    // ref https://github.com/bane-labs/go-ethereum/blob/a07310bd9a3a117ae0876ad69bbe8b6ed624aaa5/core/antimev/util.go#L27
    mapping(uint => bytes) public aggregatedCommitments;
    // hash=>used, this is used to prevent reusing and uploading the same public input
    mapping(bytes32 => bool) public isPubHashUsed;

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
        bytes calldata pubkey
    ) external {
        if (msg.sender != candidate && msg.sender != GOV)
            revert Errors.SideCallNotAllowed();
        if (tx.origin != candidate) revert Errors.OnlyEOA();
        if (pubkey.length != 65) revert Errors.InvalidMessageKey();
        messagePubkeys[candidate] = pubkey;
    }

    function reshare(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external {
        // check period
        _checkPeriodAllowed(Period.Share);

        // check index
        uint index = indexOfResharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check input format
        (uint n, uint t) = numberAndThreshold();
        if (pvss.length != (t + n + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE)
            revert Errors.InvalidPVSS();
        if (messages.length != n) revert Errors.InvalidMessageAmount();

        // verify shared pub
        uint round = roundNumber + 1;
        if (round < 2) revert Errors.InvalidRoundNumber();
        if (
            keccak256(sharedPubs[round - 1][index]) !=
            keccak256(pvss[:BLS12381.G1_SIZE])
        ) revert Errors.InvalidPVSS();

        // verify pvss
        _verifyPVSS(n, t, pvss);

        // verify public input hash
        bytes32 pubHash = _computeVerifierHashInputForShareOrReshare(
            n,
            t,
            pvss,
            messages,
            IGovernance(GOV).getPendingConsensus()
        );
        if (isPubHashUsed[pubHash]) revert Errors.PubHashAlreadyUsed();
        isPubHashUsed[pubHash] = true;

        // verify zk proof
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        SevenMessageVerifier.verifyProof(
            proof,
            commitments,
            commitmentPok,
            input
        );

        // record messages
        rpvsses[round][index] = pvss;
        reshareMsgs[round][index] = messages;

        // emit a event
        emit Reshare(round, index, msg.sender);
    }

    function share(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external {
        // check period
        _checkPeriodAllowed(Period.Share);

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

        // verify public input hash
        bytes32 pubHash = _computeVerifierHashInputForShareOrReshare(
            n,
            t,
            pvss,
            messages,
            IGovernance(GOV).getPendingConsensus()
        );
        if (isPubHashUsed[pubHash]) revert Errors.PubHashAlreadyUsed();
        isPubHashUsed[pubHash] = true;

        // verify zk proof
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        SevenMessageVerifier.verifyProof(
            proof,
            commitments,
            commitmentPok,
            input
        );

        // record messages
        uint round = roundNumber + 1;
        spvsses[round][index] = pvss;
        sharedPubs[round][index] = pvss[:BLS12381.G1_SIZE];
        shareMsgs[round][index] = messages;

        // emit a event
        emit Share(round, index, msg.sender);
    }

    function recover(
        uint[] calldata idxs,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external {
        // check period
        _checkPeriodAllowed(Period.Recover);

        // check index and round
        uint index = indexOfResharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check amount, we only have settings for 2 cases
        uint length = idxs.length;
        if (length < 1 || length > 2 || messages.length != length)
            revert Errors.InvalidMessageAmount();

        // check round
        uint round = roundNumber + 1;
        if (round < 2) revert Errors.InvalidRoundNumber();

        // verify zk proof, public hash of a recover input is allowed to be reused,
        // and the contract doesn't behave based on the recover messages
        (uint n, uint t) = numberAndThreshold();
        bytes32 pubHash = _computeVerifierHashInputForRecover(
            index,
            t,
            round,
            idxs,
            messages,
            IGovernance(GOV).getPendingConsensus()
        );
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        if (length == 1) {
            OneMessageVerifier.verifyProof(
                proof,
                commitments,
                commitmentPok,
                input
            );
        } else {
            TwoMessageVerifier.verifyProof(
                proof,
                commitments,
                commitmentPok,
                input
            );
        }

        // check absent secret index & record messages
        for (uint i = 0; i < length; i++) {
            if (idxs[i] > n || idxs[i] == 0) revert Errors.IndexOutOfRange();
            if (reshareMsgs[round][idxs[i]].length > 0)
                revert Errors.NoNeedForRecover();
            recoverMsgs[round][index][idxs[i] - 1] = messages[i];
        }

        // emit a event
        emit Recover(round, index, msg.sender);
    }

    function reshareRecovered(
        bytes calldata pvss,
        bytes[] calldata messages,
        uint[8] calldata proof,
        uint[2] calldata commitments,
        uint[2] calldata commitmentPok
    ) external {
        // check period
        _checkPeriodAllowed(Period.Recover);

        // check index
        uint index = indexOfSharing(msg.sender);
        if (index < 1) revert Errors.NotShareMember();

        // check input format
        (uint n, uint t) = numberAndThreshold();
        if (pvss.length != (t + n + 1) * BLS12381.G1_SIZE + BLS12381.G2_SIZE)
            revert Errors.InvalidPVSS();
        if (messages.length != n) revert Errors.InvalidMessageAmount();

        // verify shared pub
        uint round = roundNumber + 1;
        if (round < 2) revert Errors.InvalidRoundNumber();
        if (
            keccak256(sharedPubs[round - 1][index]) !=
            keccak256(pvss[:BLS12381.G1_SIZE])
        ) revert Errors.InvalidPVSS();

        // verify pvss
        _verifyPVSS(n, t, pvss);

        // verify public input hash
        bytes32 pubHash = _computeVerifierHashInputForShareOrReshare(
            n,
            t,
            pvss,
            messages,
            IGovernance(GOV).getPendingConsensus()
        );
        if (isPubHashUsed[pubHash]) revert Errors.PubHashAlreadyUsed();
        isPubHashUsed[pubHash] = true;

        // verify zk proof
        uint[32] memory input;
        for (uint i = 0; i < 32; i++) {
            input[i] = uint8(pubHash[i]);
        }
        SevenMessageVerifier.verifyProof(
            proof,
            commitments,
            commitmentPok,
            input
        );

        // record messages
        if (reshareMsgs[round][index].length > 0) revert Errors.MessageExists();
        rpvsses[round][index] = pvss;
        reshareMsgs[round][index] = messages;

        // emit a event
        emit Reshare(round, index, msg.sender);
    }

    // onPersistV2 is a persisting function that is called in the beginning of every
    // block by the system starting from NeoXAMEV fork.
    function onPersistV2() external {
        if (msg.sender != SYS_CALL) revert Errors.SideCallNotAllowed();
        // NOTE: should be called before Governance onPersist
        uint targetHeight = IGovernance(GOV).nextEpochStartHeight();
        if (block.number >= targetHeight) {
            if (aggregatedCommitments[roundNumber + 1].length > 0) {
                _recordAndSetToNewRound(targetHeight);
            } else {
                _dropAndSetToLatestRound(targetHeight);
            }
        }
        uint periodDuration = IGovernance(GOV).sharePeriodDuration();
        if (block.number >= targetHeight - periodDuration) {
            // return if the round key exists
            uint round = roundNumber + 1;
            if (aggregatedCommitments[round].length > 0) return;

            // check reshare and share, compute global key
            uint n = IGovernance(GOV).consensusSize();
            if (reshareMsgs[round][1].length < n && round > 1) return;
            if (shareMsgs[round][1].length < n) return;
            bytes memory output = sharedPubs[round][1];
            for (uint i = 2; i <= n; i++) {
                if (reshareMsgs[round][i].length < n && round > 1) return;
                if (shareMsgs[round][i].length < n) return;
                output = BLS12381.g1Add(output, sharedPubs[round][i]);
            }

            // record global key
            // NOTE: this not the direct key for keystore encryption, should use pk = aggregatedCommitment * scaler
            aggregatedCommitments[round] = output;
        }
    }

    function isRoundNumberIncreased(
        uint epochHeight,
        uint lastEpochHeight
    ) external view returns (bool) {
        return
            roundNumberOfEpochs[epochHeight] >
            roundNumberOfEpochs[lastEpochHeight];
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

    function _computeVerifierHashInputForShareOrReshare(
        uint n,
        uint t,
        bytes calldata pvss,
        bytes[] calldata messages,
        address[] memory participants
    ) internal view returns (bytes32) {
        // compute zk public input, 17+20+1+n*hash(64+64+96+12+1+48)
        bytes memory sumInput = bytes.concat("DKG_BATCH_HASH_V1", abi.encodePacked(msg.sender, bytes1(bytes32(n)[31])));
        for (uint i = 0; i < n; i++) {
            bytes memory input = new bytes(285);
            bytes memory pubKey = messagePubkeys[participants[i]];
            bytes memory message = messages[i];
            if (message.length != 124) revert Errors.InvalidMessage();
            uint bigFiStart = (t + 1) *
                BLS12381.G1_SIZE +
                BLS12381.G2_SIZE +
                i *
                BLS12381.G1_SIZE;
            assembly {
                // bigR bytes (64)
                mcopy(add(input, 32), add(message, 32), 64)
                // pubkey bytes (64)
                mcopy(add(input, 96), add(pubKey, 33), 64)
                // bigFi bytes (96)
                calldatacopy(
                    add(input, 160),
                    add(pvss.offset, add(bigFiStart, 16)),
                    48
                )
                calldatacopy(
                    add(input, 208),
                    add(pvss.offset, add(bigFiStart, 80)),
                    48
                )
                // nonce bytes (12)
                mcopy(add(input, 256), add(message, 96), 12)
                // encryptedFi bytes (48), gap 1 byte
                mcopy(add(input, 269), add(message, 108), 48)
            }
            // value 2 as byte (1)
            input[236] = 0x02;
            sumInput = bytes.concat(sumInput, abi.encodePacked(bytes1(bytes32(i)[31]), bytes1(uint8(32))), sha256(input));
        }
        return sha256(sumInput);
    }

    function _computeVerifierHashInputForRecover(
        uint selfIndex,
        uint t,
        uint round,
        uint[] calldata indexes,
        bytes[] calldata messages, // len(messages) == len(indexes)
        address[] memory participants // len(receivers) == n
    ) internal view returns (bytes32) {
        // compute zk public input, 17+20+1+amount*hash(64+64+96+12+1+48)
        bytes memory sumInput = bytes.concat("DKG_BATCH_HASH_V1", abi.encodePacked(msg.sender, bytes1(bytes32(indexes.length)[31])));
        for (uint i = 0; i < indexes.length; i++) {
            bytes memory input = new bytes(285);
            bytes memory pubKey = messagePubkeys[participants[indexes[i] - 1]];
            // compute bigFi from spvss commitment, bigFi = pvss.cmt.evaluate(indexes[i])
            bytes memory pvss = spvsses[round - 1][indexes[i]];
            bytes memory message = messages[i];
            if (message.length != 124) revert Errors.InvalidMessage();
            uint bigFiStart = (t + 1) *
                BLS12381.G1_SIZE +
                BLS12381.G2_SIZE +
                (selfIndex - 1) *
                BLS12381.G1_SIZE;
            assembly {
                // bigR bytes (64)
                mcopy(add(input, 32), add(message, 32), 64)
                // pubkey bytes (64)
                mcopy(add(input, 96), add(pubKey, 33), 64)
                // bigFi bytes (96)
                mcopy(add(input, 160), add(pvss, add(48, bigFiStart)), 48)
                mcopy(add(input, 208), add(pvss, add(112, bigFiStart)), 48)
                // nonce bytes (12)
                mcopy(add(input, 256), add(message, 96), 12)
                // encryptedFi bytes (48), gap 1 byte
                mcopy(add(input, 269), add(message, 108), 48)
            }
            // value 2 as byte (1)
            input[236] = 0x02;
            sumInput = bytes.concat(sumInput, abi.encodePacked(bytes1(bytes32(i)[31]), bytes1(uint8(32))), sha256(input));
        }
        return sha256(sumInput);
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
        _checkPeriodAllowed(Period.Recover);

        uint n = IGovernance(GOV).consensusSize();
        uint round = roundNumber + 1;
        uint[] memory idxs;
        // return empty if is the first round
        if (round < 2) return idxs;
        // otherwise build a dynamic array
        for (uint i = 1; i <= n; i++) {
            if (reshareMsgs[round][i].length == 0) {
                assembly {
                    mstore(idxs, add(mload(idxs), 1))
                    mstore(0x40, add(mload(0x40), 0x20))
                }
                idxs[idxs.length - 1] = i;
            }
        }
        return idxs;
    }

    function isShareReady() external view returns (bool) {
        uint n = IGovernance(GOV).consensusSize();
        uint round = roundNumber + 1;
        for (uint i = 1; i <= n; i++) {
            if (shareMsgs[round][i].length < n) {
                return false;
            }
        }
        return true;
    }

    function _checkPeriodAllowed(Period period) internal view {
        // check period
        uint targetHeight = IGovernance(GOV).nextEpochStartHeight();
        uint periodDuration = IGovernance(GOV).sharePeriodDuration();
        if (block.number < targetHeight - (uint(period) + 1) * periodDuration)
            revert Errors.PeriodNotStarted();
        if (block.number >= targetHeight - uint(period) * periodDuration)
            revert Errors.PeriodEnded();
    }

    function _recordAndSetToNewRound(uint targetHeight) internal {
        uint round = roundNumber + 1;
        // map next epoch height to new round
        roundNumberOfEpochs[targetHeight] = round;
        // increase round number
        roundNumber = round;
    }

    function _dropAndSetToLatestRound(uint targetHeight) internal {
        uint n = IGovernance(GOV).consensusSize();
        uint round = roundNumber + 1;
        // delete all uploaded data for specific round
        for (uint i = 1; i <= n; i++) {
            delete rpvsses[round][i];
            delete reshareMsgs[round][i];
            delete spvsses[round][i];
            delete shareMsgs[round][i];
            for (uint j = 1; j <= n; j++) {
                delete recoverMsgs[round][i][j];
            }
        }
        // map next epoch height to latest round
        roundNumberOfEpochs[targetHeight] = roundNumber;
    }

    function getShareMsgs(
        uint round,
        uint index
    ) external view override returns (bytes[] memory) {
        return shareMsgs[round][index];
    }

    function getReshareMsgs(
        uint round,
        uint index
    ) external view override returns (bytes[] memory) {
        return reshareMsgs[round][index];
    }
}
