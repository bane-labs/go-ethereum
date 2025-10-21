import { expect } from "chai";
import { SYS_SETTINGS, ethers, networkHelpers, allocGenesis } from "./helpers/setup.js";

describe("GovReward", function () {

    let MockCaller: any;
    let snapshot: any;
    
    before(async function () {
        await allocGenesis();
        MockCaller = await ethers.deployContract("MockFallback");
        snapshot = await networkHelpers.takeSnapshot();
    });
    
    afterEach(async function () {
        await snapshot.restore();
    });

    describe("fallback", function () {
        it("Should revert if the selector is not 0xffffffff", async function () {
            await expect(
                MockCaller.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xfffffffe")
            ).to.be.revert(ethers);
        });

        it("Should revert if the declared gaslimit is lower than 21000", async function () {
            await expect(
                MockCaller.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xffffffff")
            ).to.be.revert(ethers);
        });

        it("Should consume gas as expected", async function () {
            await expect(
                MockCaller.call_fallback(SYS_SETTINGS.REWARD_PROXY, "0xffffffff0000000000005208")
            ).not.to.be.revert(ethers);
        });
    });
});
