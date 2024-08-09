import { ethers } from "hardhat";
import { expect } from "chai";

describe("GovProxyUpgradeable", function () {
    it("Should prevent implementation contract from initialization", async function () {
        const mockGovProxyUpgradeable = await ethers.deployContract("MockGovProxyUpgradeable");
        await expect(mockGovProxyUpgradeable.initialize()).to.be.reverted;
        expect(
            await ethers.provider.send("eth_getStorageAt", [
                await mockGovProxyUpgradeable.getAddress(),
                "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00",
                "latest"]
            )
        ).to.eq("0x000000000000000000000000000000000000000000000000ffffffffffffffff");
    });

    it("Should prevent implementation contract from reinitialization", async function () {
        const mockGovProxyUpgradeable = await ethers.deployContract("MockGovProxyUpgradeable");
        await expect(mockGovProxyUpgradeable.reinitialize()).to.be.reverted;
        expect(
            await ethers.provider.send("eth_getStorageAt", [
                await mockGovProxyUpgradeable.getAddress(),
                "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00",
                "latest"]
            )
        ).to.eq("0x000000000000000000000000000000000000000000000000ffffffffffffffff");
    });
});