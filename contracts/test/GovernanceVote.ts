import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { ethers, allocGenesis } from "./helpers/setup.js";

describe("GovernanceVote", function () {

    let signers: any;

    beforeEach(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();
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
            ).not.to.be.reverted(ethers);

            expect(await Mock.v()).to.eq(0);
        });

        it("Should execute method when threshold is met", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Mock.connect(signers[i]).changeV(1)
                ).not.to.be.reverted(ethers);
            }
            expect(await Mock.v()).to.eq(0);

            await expect(
                Mock.connect(signers[3]).changeV(1)
            ).not.to.be.reverted(ethers);
            expect(await Mock.v()).to.eq(1);
        });

        it("Should clear all votes after execution", async function () {
            for (let i = 0; i < 6; i++) {
                await expect(
                    Mock.connect(signers[i]).changeV(i % 2)
                ).not.to.be.reverted(ethers);
            }

            await expect(
                Mock.connect(signers[6]).changeV(1)
            ).not.to.be.reverted(ethers);
            expect(await Mock.v()).to.eq(1);

            await expect(
                Mock.connect(signers[6]).changeV(0)
            ).not.to.be.reverted(ethers);
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
                ).not.to.be.reverted(ethers);
            }

            await expect(
                Mock.connect(signers[3]).changeV(1)
            ).emit(Mock, "Vote")
                .emit(Mock, "VotePass");
        });
    });
});
