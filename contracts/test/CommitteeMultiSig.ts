import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { ethers, networkHelpers, allocGenesis } from "./helpers/setup.js";

describe("CommitteeMultiSig", function () {

    let MultiSig: any, MockCaller: any;
    let signers: any, snapshot: any;

    before(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();
        MultiSig = await ethers.deployContract("CommitteeMultiSig");
        MockCaller = await ethers.deployContract("MockMultiSig");
        snapshot = await networkHelpers.takeSnapshot();
    });

    afterEach(async function () {
        await snapshot.restore();
    });

    describe("execute", function () {
        it("Should revert if the sender is not a miner", async function () {
            await expect(
                MultiSig.connect(signers[7]).execute(MockCaller.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).to.be.revertedWithCustomError(MultiSig, ERRORS.NOT_MINER);
        });

        it("Should not execute method when threshold is not met", async function () {
            await expect(
                MultiSig.connect(signers[0]).execute(MockCaller.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).not.to.be.revert(ethers);

            expect(await MockCaller.v()).to.eq(0);
        });

        it("Should execute method when threshold is met", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    MultiSig.connect(signers[i]).execute(MockCaller.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
                ).not.to.be.revert(ethers);
            }
            expect(await MockCaller.v()).to.eq(0);

            await expect(
                MultiSig.connect(signers[3]).execute(MockCaller.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).not.to.be.revert(ethers);
            expect(await MockCaller.v()).to.eq(1);
        });
    });
});
