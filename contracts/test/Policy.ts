import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { SYS_SETTINGS, ethers, networkHelpers, allocGenesis } from "./helpers/setup.js";

// MOCK PUBKEYS
const PUBKEY = "0x04a8c8762d32477f5bd0ccff58d35a7b7ace2fbbd0c0d61874bd405bc0af415690d16f585bcec5f51d1fdddfd0d4543cb0a9d40f0447b62a7c4b1a0f24c45ccb01";

describe("Policy", function () {

    let Policy: any, Governance: any;
    let signers: any, snapshot: any;

    before(async function () {
        signers = await ethers.getSigners();
        [Governance, , Policy,] = await allocGenesis();
        snapshot = await networkHelpers.takeSnapshot();
    });
    
    afterEach(async function () {
        await snapshot.restore();
    });

    describe("genesis", function () {
        it("Should get minimum gas tip cap as expected", async function () {
            expect(await Policy.minGasTipCap()).to.eq(SYS_SETTINGS.MIN_GAS_TIP_CAP);
        });
        it("Should get base fee as expected", async function () {
            expect(await Policy.baseFee()).to.eq(SYS_SETTINGS.BASE_FEE);
        });
        it("Should get candidate limit as expected", async function () {
            expect(await Policy.getCandidateLimit()).to.eq(SYS_SETTINGS.CANDIDATE_LIMIT);
        });
    });

    describe("addBlackList", function () {
        it("Should revert if the sender is not a miner", async function () {
            await expect(
                Policy.connect(signers[7]).addBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the address is already blacklisted", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.BLACKLIST_EXISTS);
        });

        it("Should blacklist an address if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            expect(await Policy.isBlackListed(signers[0])).to.eq(true);
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[0])
            ).emit(Policy, "AddBlackList");
        });

        it("Should deactivate governance if is a candidate", async function () {
            await Governance.connect(signers[7]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[7])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[7])
            ).emit(Governance, "Deactivate");
        });
    });

    describe("removeBlackList", function () {
        it("Should revert if the sender is not a validator", async function () {
            await expect(
                Policy.connect(signers[7]).removeBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the address is not blacklisted", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).removeBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.BLACKLIST_NOT_EXISTS);
        });

        it("Should remove a blacklist if meets the threshold", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            expect(await Policy.isBlackListed(signers[0])).to.eq(false);
        });

        it("Should emit an event if meets the threshold", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).removeBlackList(signers[0])
            ).emit(Policy, "RemoveBlackList");
        });

        it("Should activate governance if is a candidate", async function () {
            await Governance.connect(signers[7]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[7])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[7])
            ).emit(Governance, "Deactivate");

            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[7])
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).removeBlackList(signers[7])
            ).emit(Governance, "Activate");
        });
    });

    describe("setMinGasTipCap", function () {
        it("Should revert if the sender is not a validator", async function () {
            await expect(
                Policy.connect(signers[7]).setMinGasTipCap(ethers.parseEther("1"))
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the new value is 0", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setMinGasTipCap(0)
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setMinGasTipCap(0)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_MIN_GAS_TIP_CAP);
        });

        it("Should change the minimum gas tip cap if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setMinGasTipCap(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }
            expect(await Policy.minGasTipCap()).to.eq(ethers.parseEther("1"));
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setMinGasTipCap(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setMinGasTipCap(ethers.parseEther("1"))
            ).emit(Policy, "SetMinGasTipCap");
        });
    });

    describe("setBaseFee", function () {
        it("Should revert if the sender is not a validator", async function () {
            await expect(
                Policy.connect(signers[7]).setBaseFee(ethers.parseEther("1"))
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the new value is 0", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setBaseFee(0)
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setBaseFee(0)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_BASE_FEE);
        });

        it("Should change the base fee if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setBaseFee(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }
            expect(await Policy.baseFee()).to.eq(ethers.parseEther("1"));
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setBaseFee(ethers.parseEther("1"))
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setBaseFee(ethers.parseEther("1"))
            ).emit(Policy, "SetBaseFee");
        });
    });

    describe("setCandidateLimit", function () {
        it("Should revert if the sender is not a validator", async function () {
            await expect(
                Policy.connect(signers[7]).setCandidateLimit(2001)
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the new value is lower than consensus size", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setCandidateLimit(6)
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setCandidateLimit(6)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_CANDIDATE_LIMIT);
        });

        it("Should change the candidate limit if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setCandidateLimit(2001)
                ).not.to.be.revert(ethers);
            }
            expect(await Policy.getCandidateLimit()).to.eq(2001);
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setCandidateLimit(2001)
                ).not.to.be.revert(ethers);
            }
            await expect(
                Policy.connect(signers[3]).setCandidateLimit(2001)
            ).emit(Policy, "SetCandidateLimit");
        });
    });

    describe("getCandidateLimit", function () {
        it("Should return default value if not setted", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0x4", ethers.toBeHex(0, 32)]);
            expect(await Policy.getCandidateLimit()).to.eq(SYS_SETTINGS.CANDIDATE_LIMIT);
        });
    });
});
