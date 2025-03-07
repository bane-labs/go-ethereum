import { ethers } from "hardhat";
import { expect } from "chai";
import { ERRORS } from "./helpers/errors";

// NATIVE ADDRESSES
const GOV_PROXY = "0x1212000000000000000000000000000000000001";
const REWARD_PROXY = "0x1212000000000000000000000000000000000003";

// CONFIG
const CONSENSUS_SIZE = 7;
const MIN_VOTE_AMOUNT = ethers.parseEther("1");
const VOTE_TARGET_AMOUNT = 3000000;
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

describe("GovernanceVote", function () {

    let Governance: any;
    let signers: any;

    beforeEach(async function () {
        // Signers
        signers = await ethers.getSigners();

        // Reset blockchain state
        await ethers.provider.send("hardhat_reset")

        // Deploy contract
        const governance_deploy = await ethers.deployContract("Governance");
        const reward_deploy = await ethers.deployContract("GovReward");

        // Copy Bytecode to native address
        const governance_code = await ethers.provider.send("eth_getCode", [governance_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [GOV_PROXY, governance_code]);
        const governance_contract = require("../artifacts/solidity/Governance.sol/Governance.json");
        Governance = new ethers.Contract(GOV_PROXY, governance_contract.abi, signers[0]);

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

    describe("needVote", function () {
        let Mock: any;

        beforeEach(async function () {
            Mock = await ethers.deployContract("MockGovVote");
        });

        it("Should revert if the sender is not a miner", async function () {
            await expect(
                Mock.connect(signers[7]).changeV(1)
            ).to.be.revertedWithCustomError(Mock, ERRORS.NOT_MINER);
        });

        it("Should not execute method when threshold is not met", async function () {
            await expect(
                Mock.connect(signers[0]).changeV(1)
            ).not.to.be.reverted;

            expect(await Mock.v()).to.eq(0);
        });

        it("Should execute method when threshold is met", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Mock.connect(signers[i]).changeV(1)
                ).not.to.be.reverted;
            }
            expect(await Mock.v()).to.eq(0);

            await expect(
                Mock.connect(signers[3]).changeV(1)
            ).not.to.be.reverted;
            expect(await Mock.v()).to.eq(1);
        });

        it("Should clear all votes after execution", async function () {
            for (let i = 0; i < 6; i++) {
                await expect(
                    Mock.connect(signers[i]).changeV(i % 2)
                ).not.to.be.reverted;
            }

            await expect(
                Mock.connect(signers[6]).changeV(1)
            ).not.to.be.reverted;
            expect(await Mock.v()).to.eq(1);

            await expect(
                Mock.connect(signers[6]).changeV(0)
            ).not.to.be.reverted;
            expect(await Mock.v()).to.eq(1);
        });

        it("Should emit an event when a miner votes", async function () {
            await expect(
                Mock.connect(signers[0]).changeV(1)
            ).emit(Mock, "Vote");
        });

        it("Should emit an event when a vote passes", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Mock.connect(signers[i]).changeV(1)
                ).not.to.be.reverted;
            }

            await expect(
                Mock.connect(signers[3]).changeV(1)
            ).emit(Mock, "Vote")
                .emit(Mock, "VotePass");
        });
    });
});
