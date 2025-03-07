import { ethers } from "hardhat";
import { expect } from "chai";

const REWARD_PROXY = "0x1212000000000000000000000000000000000003";

describe("GovReward", function () {
    let Reward: any;
    let signers: any;

    beforeEach(async function () {
        // Signers
        signers = await ethers.getSigners();

        // Reset blockchain state
        await ethers.provider.send("hardhat_reset")

        // Deploy contract
        const reward_deploy = await ethers.deployContract("GovReward");

        // Copy Bytecode to native address
        const reward_code = await ethers.provider.send("eth_getCode", [reward_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, reward_code]);

        const contract = require("../artifacts/solidity/GovReward.sol/GovReward.json");
        Reward = new ethers.Contract(REWARD_PROXY, contract.abi, signers[0]);
    });

    describe("envelope call", function () {
        let Mock: any;

        beforeEach(async function () {
            Mock = await ethers.deployContract("MockFallback");
        });

        it("Should revert if the selector is not 0xffffffff", async function () {
            await expect(
                Mock.call_fallback(REWARD_PROXY, "0xfffffffe")
            ).to.be.reverted;
        });

        it("Should revert if the declared gaslimit is lower than 21000", async function () {
            await expect(
                Mock.call_fallback(REWARD_PROXY, "0xffffffff")
            ).to.be.reverted;
        });

        it("Should consume gas as expected", async function () {
            await expect(
                Mock.call_fallback(REWARD_PROXY, "0xffffffff0000000000005208")
            ).not.to.be.reverted;
        });
    });
});
