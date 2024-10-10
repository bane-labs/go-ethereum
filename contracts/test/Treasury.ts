import { ethers } from "hardhat";
import { expect } from "chai";
import { ERRORS } from "./helpers/errors";

// NATIVE ADDRESSES
const GOV_ADMIN = "0x1212000000000000000000000000000000000000";
const GOV_PROXY = "0x1212000000000000000000000000000000000001";
const GOV_IMP = "0x1212100000000000000000000000000000000001";
const POLICY_PROXY = "0x1212000000000000000000000000000000000002";
const POLICY_IMP = "0x1212100000000000000000000000000000000002";
const REWARD_PROXY = "0x1212000000000000000000000000000000000003";
const BRIDGE_PROXY = "0x1212000000000000000000000000000000000004";
const REWARD_IMP = "0x1212100000000000000000000000000000000003";
const SYS_CALL = "0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE";

// CONFIG
const CONSENSUS_SIZE = 7;
const MIN_VOTE_AMOUNT = ethers.parseEther("1");
const VOTE_TARGET_AMOUNT = ethers.parseEther("3000");
const REGISTER_FEE = ethers.parseEther("1000");
const EPOCH_DURATION = 60480;
const SHARE_PERIOD = 180;
const STANDBY_VALIDATORS = [
    "0xcbbeca26e89011e32ba25610520b20741b809007",
    "0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc",
    "0xd10f47396dc6c76ad53546158751582d3e2683ef",
    "0xa51fe05b0183d01607bf48c1718d1168a1c11171",
    "0x01b517b301bb143476da35bb4a1399500d925514",
    "0x7976ad987d572377d39fb4bab86c80e08b6f8327",
    "0xd711da2d8c71a801fc351163337656f1321343a0"
];

describe("Treasury", function () {

    let Treasury: any;
    let signers: any;

    beforeEach(async function () {
        // Signers
        signers = await ethers.getSigners();

        // Reset blockchain state
        await ethers.provider.send("hardhat_reset")

        // Deploy contract
        const governance_deploy = await ethers.deployContract("Governance");
        const reward_deploy = await ethers.deployContract("GovReward");
        Treasury = await ethers.deployContract("Treasury");

        // Copy Bytecode to native address
        const governance_code = await ethers.provider.send("eth_getCode", [governance_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [GOV_PROXY, governance_code]);

        const reward_code = await ethers.provider.send("eth_getCode", [reward_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, reward_code]);

        // Write Governance config to storage
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x2", ethers.toBeHex(MIN_VOTE_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x3", ethers.toBeHex(VOTE_TARGET_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x4", ethers.toBeHex(REGISTER_FEE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x5", ethers.toBeHex(EPOCH_DURATION, 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x10", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae672", ethers.toBeHex(signers[0].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae673", ethers.toBeHex(signers[1].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae674", ethers.toBeHex(signers[2].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae675", ethers.toBeHex(signers[3].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae676", ethers.toBeHex(signers[4].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae677", ethers.toBeHex(signers[5].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae678", ethers.toBeHex(signers[6].address, 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x11", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c68", ethers.toBeHex(STANDBY_VALIDATORS[0], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c69", ethers.toBeHex(STANDBY_VALIDATORS[1], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6a", ethers.toBeHex(STANDBY_VALIDATORS[2], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6b", ethers.toBeHex(STANDBY_VALIDATORS[3], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6c", ethers.toBeHex(STANDBY_VALIDATORS[4], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6d", ethers.toBeHex(STANDBY_VALIDATORS[5], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6e", ethers.toBeHex(STANDBY_VALIDATORS[6], 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x17", ethers.toBeHex(SHARE_PERIOD, 32)]);
    });

    describe("fundBridge", function () {
        let Mock: any;

        beforeEach(async function () {
            const mock_deploy = await ethers.deployContract("MockBridge");

            // Copy Bytecode to native address
            const mock_code = await ethers.provider.send("eth_getCode", [mock_deploy.target]);
            await ethers.provider.send("hardhat_setCode", [BRIDGE_PROXY, mock_code]);
            const mock_contract = require("../artifacts/solidity/test/MockBridge.sol/MockBridge.json");
            Mock = new ethers.Contract(BRIDGE_PROXY, mock_contract.abi, signers[0]);

            await ethers.provider.send("hardhat_setStorageAt", [BRIDGE_PROXY, "0x0", ethers.toBeHex(Treasury.target, 32)]);
            await ethers.provider.send("hardhat_setBalance", [Treasury.target, ethers.toBeHex(ethers.parseEther("1"), 32)]);
        });

        it("Should not release GAS when threshold is not met", async function () {
            await expect(
                Treasury.connect(signers[0]).fundBridge(ethers.parseEther("1"))
            ).not.to.be.reverted;

            expect(await ethers.provider.getBalance(Mock.target)).to.eq(0);
        });

        it("Should release GAS if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }

            expect(await ethers.provider.getBalance(Mock.target)).to.eq(ethers.parseEther("1"));
        });

        it("Should revert if transfer fails", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("10"))
                ).not.to.be.reverted;
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("10"))
            ).to.be.revertedWithCustomError(Treasury, ERRORS.TRANSFER_FAILED);
        });

        it("Should emit an event when a transfer succeeds", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("1"))
            ).emit(Treasury, "BridgeFund");
        });
    });
});