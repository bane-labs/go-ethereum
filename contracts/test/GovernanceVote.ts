import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { ethers, networkHelpers, allocGenesis } from "./helpers/setup.js";

describe("GovernanceVote", function () {

    let MockCaller: any;
    let signers: any, snapshot: any;

    before(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();
        MockCaller = await ethers.deployContract("MockGovVote");
        snapshot = await networkHelpers.takeSnapshot();
    });

    afterEach(async function () {
        await snapshot.restore();
    });

    describe("needVote", function () {
        it("Should revert if the sender is not a miner", async function () {
            await expect(
                MockCaller.connect(signers[7]).changeV(1)
            ).to.be.revertedWithCustomError(MockCaller, ERRORS.NOT_MINER);
        });

        it("Should not execute method when threshold is not met", async function () {
            await expect(
                MockCaller.connect(signers[0]).changeV(1)
            ).not.to.be.revert(ethers);

            expect(await MockCaller.v()).to.eq(0);
        });

        it("Should execute method when threshold is met", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    MockCaller.connect(signers[i]).changeV(1)
                ).not.to.be.revert(ethers);
            }
            expect(await MockCaller.v()).to.eq(0);

            await expect(
                MockCaller.connect(signers[3]).changeV(1)
            ).not.to.be.revert(ethers);
            expect(await MockCaller.v()).to.eq(1);
        });

        it("Should clear all votes after execution", async function () {
            for (let i = 0; i < 6; i++) {
                await expect(
                    MockCaller.connect(signers[i]).changeV(i % 2)
                ).not.to.be.revert(ethers);
            }

            await expect(
                MockCaller.connect(signers[6]).changeV(1)
            ).not.to.be.revert(ethers);
            expect(await MockCaller.v()).to.eq(1);

            await expect(
                MockCaller.connect(signers[6]).changeV(0)
            ).not.to.be.revert(ethers);
            expect(await MockCaller.v()).to.eq(1);
        });

        it("Should emit an event when a miner votes", async function () {
            await expect(
                MockCaller.connect(signers[0]).changeV(1)
            ).emit(MockCaller, "Vote");
        });

        it("Should emit an event when a vote passes", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    MockCaller.connect(signers[i]).changeV(1)
                ).not.to.be.revert(ethers);
            }

            await expect(
                MockCaller.connect(signers[3]).changeV(1)
            ).emit(MockCaller, "Vote")
                .emit(MockCaller, "VotePass");
        });
    });
});
