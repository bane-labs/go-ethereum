import { expect } from "chai";
import { SYS_SETTINGS, ethers, networkHelpers, allocGenesis } from "./helpers/setup.js";

describe("GovPaymaster", function () {
    let Policy: any;
    let MockEntryPoint: any, MockSysCall: any, MockAccount: any;
    let signers: any, snapshot: any;

    interface PackedUserOperation {
        sender: string
        nonce: number
        initCode: string
        callData: string
        accountGasLimits: string
        preVerificationGas: number
        gasFees: string
        paymasterAndData: string
        signature: string
    }

    const buildUserOp = (overrides: Record<string, any> = {}): PackedUserOperation => ({
        sender: MockAccount.target,
        nonce: 0,
        initCode: "0x",
        callData: "0x",
        accountGasLimits: ethers.solidityPacked(["uint128", "uint128"], [100_000, 100_000]),
        preVerificationGas: 100_000,
        gasFees: ethers.solidityPacked(["uint128", "uint128"], [SYS_SETTINGS.MIN_GAS_TIP_CAP, SYS_SETTINGS.BASE_FEE + SYS_SETTINGS.MIN_GAS_TIP_CAP]),
        paymasterAndData: ethers.solidityPacked(["address", "uint128", "uint128"], [SYS_SETTINGS.PAYMASTER_PROXY, 100_000, 100_000]),
        signature: "0x",
        ...overrides,
    });

    before(async function () {
        signers = await ethers.getSigners();
        [, , Policy,] = await allocGenesis();

        // Set the reward share rate
        await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0x8", ethers.toBeHex(SYS_SETTINGS.SPONSOR_RATE, 32)]);

        // Deploy Mock EntryPoint
        const pm_mock = await ethers.deployContract("MockEntryPoint");
        const pm_code = await ethers.provider.send("eth_getCode", [pm_mock.target]);
        await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.ENTRY_POINT, pm_code]);
        MockEntryPoint = await ethers.getContractAt("MockEntryPoint", SYS_SETTINGS.ENTRY_POINT, signers[0]);

        // Deploy Mock SYS_CALL
        const sys_mock = await ethers.deployContract("MockSysCall");
        const sys_code = await ethers.provider.send("eth_getCode", [sys_mock.target]);
        await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.SYS_CALL, sys_code]);
        MockSysCall = await ethers.getContractAt("MockSysCall", SYS_SETTINGS.SYS_CALL, signers[0]);

        // Deploy Mock Account
        MockAccount = await ethers.deployContract("MockAccount");

        snapshot = await networkHelpers.takeSnapshot();
    });

    afterEach(async function () {
        await snapshot.restore();
    });

    describe("receive", function () {
        it("Should deposit when receive rewards", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.REWARD_PROXY,
                value: ethers.parseEther("0.2"),
            });
            await tx.wait();
            await expect(
                await MockSysCall.call_onPersist(SYS_SETTINGS.GOV_PROXY)
            ).to.emit(MockEntryPoint, "Deposited");

            // Check deposit
            expect(await MockEntryPoint.balanceOf(SYS_SETTINGS.PAYMASTER_PROXY)).to.eq(ethers.parseEther("0.1"));
        });

        it("Should deposit up to 0.4 GAS in the EntryPoint", async function () {
            const tx1 = await signers[0].sendTransaction({
                to: SYS_SETTINGS.REWARD_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx1.wait();
            await expect(
                await MockSysCall.call_onPersist(SYS_SETTINGS.GOV_PROXY)
            ).to.emit(MockEntryPoint, "Deposited");

            // Check deposit
            expect(await MockEntryPoint.balanceOf(SYS_SETTINGS.PAYMASTER_PROXY)).to.eq(ethers.parseEther("0.4"));

            const tx2 = await signers[0].sendTransaction({
                to: SYS_SETTINGS.REWARD_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx2.wait();
            await expect(
                await MockSysCall.call_onPersist(SYS_SETTINGS.GOV_PROXY)
            ).not.to.emit(MockEntryPoint, "Deposited");

            // Check deposit
            expect(await MockEntryPoint.balanceOf(SYS_SETTINGS.PAYMASTER_PROXY)).to.eq(ethers.parseEther("0.4"));
        });
    });

    describe("validatePaymasterUserOp", function () {
        it("Should revert if sender is blacklisted", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.PAYMASTER_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx.wait();

            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(MockAccount)
                ).not.to.be.revert(ethers);
            }

            const userOp = buildUserOp();
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).to.be.revert(ethers);
        });

        it("Should revert if gas tip cap is higher than 120% policy", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.PAYMASTER_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx.wait();

            const userOp = buildUserOp({
                gasFees: ethers.solidityPacked(["uint128", "uint128"], [ethers.parseUnits("25", "gwei"), ethers.parseUnits("45", "gwei")]),
            });
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).to.be.revert(ethers);
        });

        it("Should revert if gas price is higher than the allowed limit", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.PAYMASTER_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx.wait();

            const userOp = buildUserOp({
                gasFees: ethers.solidityPacked(["uint128", "uint128"], [ethers.parseUnits("24", "gwei"), ethers.parseUnits("54", "gwei")]),
            });
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).to.be.revert(ethers);
        });

        it("Should revert if fee cost is higher than the limit", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.PAYMASTER_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx.wait();

            const userOp = buildUserOp({
                preVerificationGas: 1_000_000_000,
                accountGasLimits: ethers.solidityPacked(["uint128", "uint128"], [10_000_000, 10_000_000]),
            });
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).to.be.revert(ethers);
        });

        it("Should revert if deposit balance is insufficient", async function () {
            const userOp = buildUserOp();
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).to.be.revert(ethers);
        });

        it("Should execute the user operation if meets the requirements", async function () {
            const tx = await signers[0].sendTransaction({
                to: SYS_SETTINGS.PAYMASTER_PROXY,
                value: ethers.parseEther("1"),
            });
            await tx.wait();

            const userOp = buildUserOp();
            await expect(MockEntryPoint.handleOps([userOp], signers[0].address)).not.to.be.revert(ethers);
        });
    });
});
