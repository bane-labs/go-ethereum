import { network } from "hardhat";
import { expect } from "chai";

const { ethers } = await network.connect();

describe("GovProxyUpgradeable", function () {

    let Mock: any;

    before(async function () {
        Mock = await ethers.deployContract("MockGovProxyUpgradeable");
    });

    it("Should prevent implementation contract from initialization", async function () {
        await expect(Mock.initialize()).to.be.revert(ethers);
        expect(
            await ethers.provider.send("eth_getStorageAt", [
                await Mock.getAddress(),
                "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00",
                "latest"]
            )
        ).to.eq("0x000000000000000000000000000000000000000000000000ffffffffffffffff");
    });

    it("Should prevent implementation contract from reinitialization", async function () {
        await expect(Mock.reinitialize()).to.be.revert(ethers);
        expect(
            await ethers.provider.send("eth_getStorageAt", [
                await Mock.getAddress(),
                "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00",
                "latest"]
            )
        ).to.eq("0x000000000000000000000000000000000000000000000000ffffffffffffffff");
    });
});
