import { ethers } from "hardhat";
import { expect } from "chai";
import { ERRORS } from "./helpers/errors";
import { SYS_SETTINGS, allocGenesis } from "./helpers/setup";

describe("Treasury", function () {

    let Treasury: any;
    let signers: any;

    beforeEach(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();

        Treasury = await ethers.deployContract("Treasury");
    });

    describe("fundBridge", function () {
        let Mock: any;

        beforeEach(async function () {
            const mock_deploy = await ethers.deployContract("MockBridge");

            // Copy Bytecode to native address
            const mock_code = await ethers.provider.send("eth_getCode", [mock_deploy.target]);
            await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.BRIDGE_PROXY, mock_code]);
            const mock_contract = require("../artifacts/solidity/test/MockBridge.sol/MockBridge.json");
            Mock = new ethers.Contract(SYS_SETTINGS.BRIDGE_PROXY, mock_contract.abi, signers[0]);

            await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.BRIDGE_PROXY, "0x0", ethers.toBeHex(Treasury.target, 32)]);
            await ethers.provider.send("hardhat_setBalance", [Treasury.target, ethers.toBeHex(ethers.parseEther("1"), 32)]);
        });

        it("Should not release GAS when threshold is not met", async function () {
            await expect(
                Treasury.connect(signers[0]).fundBridge(ethers.parseEther("1"))
            ).not.to.be.reverted;

            expect(await ethers.provider.getBalance(Mock.target)).to.eq(0);
        });

        it("Should release GAS if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }

            expect(await ethers.provider.getBalance(Mock.target)).to.eq(ethers.parseEther("1"));
        });

        it("Should revert if transfer fails", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("10"))
                ).not.to.be.reverted;
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("10"))
            ).to.be.revertedWithCustomError(Treasury, ERRORS.TRANSFER_FAILED);
        });

        it("Should emit an event when a transfer succeeds", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("1"))
            ).emit(Treasury, "BridgeFund");
        });
    });
});