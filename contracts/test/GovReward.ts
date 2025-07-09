import { expect } from "chai";
import { SYS_SETTINGS, ethers, allocGenesis } from "./helpers/setup.js";

describe("GovReward", function () {

    beforeEach(async function () {
        await allocGenesis();
    });

    describe("fallback", function () {
        let Mock: any;

        beforeEach(async function () {
            Mock = await ethers.deployContract("MockFallback");
        });

        it("Should revert if the selector is not 0xffffffff", async function () {
            await expect(
                Mock.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xfffffffe")
            ).to.be.reverted(ethers);
        });

        it("Should revert if the declared gaslimit is lower than 21000", async function () {
            await expect(
                Mock.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xffffffff")
            ).to.be.reverted(ethers);
        });

        it("Should consume gas as expected", async function () {
            await expect(
                Mock.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xffffffff0000000000005208")
            ).not.to.be.reverted(ethers);
        });
    });
});
