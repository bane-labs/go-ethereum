import { ethers } from "hardhat";
import { expect } from "chai";
import { ERRORS } from "./helpers/errors";
import { allocGenesis } from "./helpers/setup";

describe("CommitteeMultiSig", function () {

    let signers: any;

    beforeEach(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();
    });

    describe("execute", function () {
        let MultiSig: any, Mock: any;

        beforeEach(async function () {
            MultiSig = await ethers.deployContract("CommitteeMultiSig");
            Mock = await ethers.deployContract("MockMultiSig");
        });

        it("Should revert if the sender is not a miner", async function () {
            await expect(
                MultiSig.connect(signers[7]).execute(Mock.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).to.be.revertedWithCustomError(MultiSig, ERRORS.NOT_MINER);
        });

        it("Should not execute method when threshold is not met", async function () {
            await expect(
                MultiSig.connect(signers[0]).execute(Mock.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).not.to.be.reverted;

            expect(await Mock.v()).to.eq(0);
        });

        it("Should execute method when threshold is met", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    MultiSig.connect(signers[i]).execute(Mock.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
                ).not.to.be.reverted;
            }
            expect(await Mock.v()).to.eq(0);

            await expect(
                MultiSig.connect(signers[3]).execute(Mock.target, "0xa1b2ca7d0000000000000000000000000000000000000000000000000000000000000001")
            ).not.to.be.reverted;
            expect(await Mock.v()).to.eq(1);
        });
    });
});