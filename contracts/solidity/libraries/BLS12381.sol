// SPDX-License-Identifier: MIT
pragma solidity ^0.8.25;

library BLS12381 {
    uint public constant SCALAR_SIZE = 32;
    uint public constant G1_SIZE = 128;
    uint public constant G2_SIZE = 256;

    // g1.One
    bytes internal constant _g1One =
        hex"0000000000000000000000000000000017f1d3a73197d7942695638c4fa9ac0fc3688c4f9774b905a14e3a3f171bac586c55e83ff97a1aeffb3af00adb22c6bb0000000000000000000000000000000008b3f481e3aaa0f1a09e30ed741d8ae4fcf5e095d5d00af600db18cb2c04b3edd03cc744a2888ae40caa232946c5e7e1";

    // g2.One
    bytes internal constant _g2One =
        hex"00000000000000000000000000000000024aa2b2f08f0a91260805272dc51051c6e47ad4fa403b02b4510b647ae3d1770bac0326a805bbefd48056c8c121bdb80000000000000000000000000000000013e02b6052719f607dacd3a088274f65596bd0d09920b61ab5da61bbdc7f5049334cf11213945d57e5ac7d055d042b7e000000000000000000000000000000000ce5d527727d6e118cc9cdc6da2e351aadfd9baa8cbdd3a76d429a695160d12c923ac9cc3baca289e193548608b82801000000000000000000000000000000000606c4a02ea734cc32acd2b02bc28b99cb3e287e85a763af267492ab572e99ab3f370d275cec1da1aaa9075ff05f79be";

    function g1One() internal pure returns(bytes memory) {
        return _g1One;
    }

    function g2One() internal pure returns(bytes memory) {
        return _g2One;
    }

    function g1Add(
        bytes memory a, // g1 point, 128 bytes
        bytes memory b // g1 point, 128 bytes
    ) internal view returns (bytes memory c) {
        bytes memory input = bytes.concat(a, b);
        require(input.length == 256, "_g1Add malformed input");
        bool success;
        c = new bytes(128);
        assembly {
            success := staticcall(
                gas(),
                0x0b,
                add(input, 0x20),
                256,
                add(c, 0x20),
                128
            )
            switch success
            case 0 {
                invalid()
            }
        }
    }

    function g1Mul(
        bytes memory point, // g1 point, 128 bytes
        uint256 scalar // big int, 32 bytes
    ) internal view returns (bytes memory) {
        bytes memory input = abi.encodePacked(point, scalar);
        require(input.length == 160, "_g1Mul malformed input");
        return g1MultiExp(input);
    }

    function g1MultiExp(
        bytes memory input // (g1 point, big int) pairs, (128 + 32) * n bytes
    ) internal view returns (bytes memory c) {
        require(input.length % 160 == 0, "_g1MultiExp malformed input");
        bool success;
        c = new bytes(128);
        assembly {
            success := staticcall(
                gas(),
                0x0c,
                add(input, 0x20),
                mload(input),
                add(c, 0x20),
                128
            )
            switch success
            case 0 {
                invalid()
            }
        }
    }

    function checkPairing(
        bytes memory input // (g1 point, g2 point) pairs, (128 + 256) * n bytes
    ) internal view returns (bool) {
        require(input.length % 384 == 0, "_checkPairing malformed input");
        bytes memory res = new bytes(32);
        bool success;
        assembly {
            success := staticcall(
                gas(),
                0x0f,
                add(input, 0x20),
                mload(input),
                add(res, 0x20),
                32
            )
            switch success
            case 0 {
                invalid()
            }
        }
        return res[31] != 0;
    }
}
