import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { ethers, SYS_SETTINGS, allocGenesis, networkHelpers } from "./helpers/setup.js";

describe("Treasury", function () {

    let Treasury: any, MockBridge: any;
    let signers: any, snapshot: any;

    before(async function () {
        signers = await ethers.getSigners();
        await allocGenesis();

        // Deploy Treasury and MockBridge
        Treasury = await ethers.deployContract("Treasury");
        await ethers.provider.send("hardhat_setBalance", [Treasury.target, ethers.toBeHex(ethers.parseEther("1"), 32)]);

        const deploy = await ethers.deployContract("MockBridge");
        const code = await ethers.provider.send("eth_getCode", [deploy.target]);
        await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.BRIDGE_PROXY, code]);
        MockBridge = await ethers.getContractAt("MockBridge", SYS_SETTINGS.BRIDGE_PROXY, signers[0]);
        await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.BRIDGE_PROXY, "0x0", ethers.toBeHex(Treasury.target, 32)]);

        snapshot = await networkHelpers.takeSnapshot();
    });

    afterEach(async function () {
        await snapshot.restore();
    });

    describe("fundBridge", function () {
        it("Should not release GAS when threshold is not met", async function () {
            await expect(
                Treasury.connect(signers[0]).fundBridge(ethers.parseEther("1"))
            ).not.to.be.revert(ethers);

            expect(await ethers.provider.getBalance(MockBridge.target)).to.eq(0);
        });

        it("Should release GAS if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }

            expect(await ethers.provider.getBalance(MockBridge.target)).to.eq(ethers.parseEther("1"));
        });

        it("Should revert if transfer fails", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("10"))
                ).not.to.be.revert(ethers);
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("10"))
            ).to.be.revertedWithCustomError(Treasury, ERRORS.TRANSFER_FAILED);
        });

        it("Should emit an event when a transfer succeeds", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Treasury.connect(signers[i]).fundBridge(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }

            await expect(
                Treasury.connect(signers[3]).fundBridge(ethers.parseEther("1"))
            ).emit(Treasury, "BridgeFund");
        });
    });
});
