import { ethers } from "hardhat";
import { expect } from "chai";
import { ERRORS } from "./helpers/errors";

// NATIVE ADDRESSES
const GOV_PROXY = "0x1212000000000000000000000000000000000001";
const POLICY_PROXY = "0x1212000000000000000000000000000000000002";
const REWARD_PROXY = "0x1212000000000000000000000000000000000003";
const KEYMANAGEMENT_PROXY = "0x1212000000000000000000000000000000000008";

// CONFIG
const CONSENSUS_SIZE = 7;
const MIN_VOTE_AMOUNT = ethers.parseEther("1");
const VOTE_TARGET_AMOUNT = 3000000;
const REGISTER_FEE = ethers.parseEther("1000");
const EPOCH_DURATION = 60480;
const SHARE_PERIOD = 180;
const STANDBY_VALIDATORS = [
    "0xcbbeca26e89011e32ba25610520b20741b809007",
    "0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc",
    "0xd10f47396dc6c76ad53546158751582d3e2683ef",
    "0xa51fe05b0183d01607bf48c1718d1168a1c11171",
    "0x01b517b301bb143476da35bb4a1399500d925514",
    "0x7976ad987d572377d39fb4bab86c80e08b6f8327",
    "0xd711da2d8c71a801fc351163337656f1321343a0"
];

const MIN_GAS_TIP_CAP = ethers.parseUnits("1", "gwei");
const BASE_FEE = ethers.parseUnits("1", "gwei");
const CANDIDATE_LIMIT = 2000;

// MOCK PUBKEYS
const PUBKEY = "0x04a8c8762d32477f5bd0ccff58d35a7b7ace2fbbd0c0d61874bd405bc0af415690d16f585bcec5f51d1fdddfd0d4543cb0a9d40f0447b62a7c4b1a0f24c45ccb01";

describe("Policy", function () {

    let Policy: any, Governance: any;
    let signers: any;

    beforeEach(async function () {
        // Signers
        signers = await ethers.getSigners();

        // Reset blockchain state
        await ethers.provider.send("hardhat_reset")

        // Deploy libraries need link
        const verifier1 = await ethers.deployContract("OneMessageVerifier");
        const verifier2 = await ethers.deployContract("TwoMessageVerifier");
        const verifier3 = await ethers.deployContract("SevenMessageVerifier");

        // Deploy Governance contract
        const governance_deploy = await ethers.deployContract("Governance");
        const reward_deploy = await ethers.deployContract("GovReward");
        const policy_deploy = await ethers.deployContract("Policy");
        const keymanagement_deploy = await ethers.deployContract("KeyManagement", {
            libraries: {
                OneMessageVerifier: verifier1.target,
                TwoMessageVerifier: verifier2.target,
                SevenMessageVerifier: verifier3.target,
            }
        });

        // Copy Bytecode to native address
        const governance_code = await ethers.provider.send("eth_getCode", [governance_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [GOV_PROXY, governance_code]);

        const reward_code = await ethers.provider.send("eth_getCode", [reward_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, reward_code]);

        const policy_code = await ethers.provider.send("eth_getCode", [policy_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [POLICY_PROXY, policy_code]);

        const keymanagement_code = await ethers.provider.send("eth_getCode", [keymanagement_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [KEYMANAGEMENT_PROXY, keymanagement_code]);

        const governance_contract = require("../artifacts/solidity/Governance.sol/Governance.json");
        Governance = new ethers.Contract(GOV_PROXY, governance_contract.abi, signers[0]);
        const contract = require("../artifacts/solidity/Policy.sol/Policy.json");
        Policy = new ethers.Contract(POLICY_PROXY, contract.abi, signers[0]);

        // Write Governance config to storage
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x2", ethers.toBeHex(MIN_VOTE_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x3", ethers.toBeHex(VOTE_TARGET_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x4", ethers.toBeHex(REGISTER_FEE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x5", ethers.toBeHex(EPOCH_DURATION, 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x10", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae672", ethers.toBeHex(signers[0].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae673", ethers.toBeHex(signers[1].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae674", ethers.toBeHex(signers[2].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae675", ethers.toBeHex(signers[3].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae676", ethers.toBeHex(signers[4].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae677", ethers.toBeHex(signers[5].address, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae678", ethers.toBeHex(signers[6].address, 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x11", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c68", ethers.toBeHex(STANDBY_VALIDATORS[0], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c69", ethers.toBeHex(STANDBY_VALIDATORS[1], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6a", ethers.toBeHex(STANDBY_VALIDATORS[2], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6b", ethers.toBeHex(STANDBY_VALIDATORS[3], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6c", ethers.toBeHex(STANDBY_VALIDATORS[4], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6d", ethers.toBeHex(STANDBY_VALIDATORS[5], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6e", ethers.toBeHex(STANDBY_VALIDATORS[6], 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x17", ethers.toBeHex(SHARE_PERIOD, 32)]);

        // Write Policy config to storage
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x2", ethers.toBeHex(MIN_GAS_TIP_CAP, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x3", ethers.toBeHex(BASE_FEE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x4", ethers.toBeHex(CANDIDATE_LIMIT, 32)]);
    });

    describe("genesis", function () {
        it("Should get minimum gas tip cap as expected", async function () {
            expect(await Policy.minGasTipCap()).to.eq(MIN_GAS_TIP_CAP);
        });
        it("Should get base fee as expected", async function () {
            expect(await Policy.baseFee()).to.eq(BASE_FEE);
        });
        it("Should get candidate limit as expected", async function () {
            expect(await Policy.getCandidateLimit()).to.eq(CANDIDATE_LIMIT);
        });
    });

    describe("addBlackList", function () {
        it("Should revert if the sender is not a miner", async function () {
            await expect(
                Policy.connect(signers[7]).addBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.NOT_MINER);
        });

        it("Should revert if the address is already blacklisted", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.BLACKLIST_EXISTS);
        });

        it("Should blacklist an address if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.reverted;
            }
            expect(await Policy.isBlackListed(signers[0])).to.eq(true);
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[0])
            ).emit(Policy, "AddBlackList");
        });

        it("Should deactivate governance if is a candidate", async function () {
            await Governance.connect(signers[7]).registerCandidate(500, PUBKEY, { value: REGISTER_FEE });
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[7])
                ).not.to.be.reverted;
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
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).removeBlackList(signers[0])
            ).to.be.revertedWithCustomError(Policy, ERRORS.BLACKLIST_NOT_EXISTS);
        });

        it("Should remove a blacklist if meets the threshold", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.reverted;
            }
            expect(await Policy.isBlackListed(signers[0])).to.eq(false);
        });

        it("Should emit an event if meets the threshold", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0xa3c1274aadd82e4d12c8004c33fb244ca686dad4fcc8957fc5668588c11d9502", ethers.toBeHex(1, 32)]);
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).removeBlackList(signers[0])
            ).emit(Policy, "RemoveBlackList");
        });

        it("Should activate governance if is a candidate", async function () {
            await Governance.connect(signers[7]).registerCandidate(500, PUBKEY, { value: REGISTER_FEE });
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[7])
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).addBlackList(signers[7])
            ).emit(Governance, "Deactivate");

            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[7])
                ).not.to.be.reverted;
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
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).setMinGasTipCap(0)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_MIN_GAS_TIP_CAP);
        });

        it("Should change the minimum gas tip cap if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setMinGasTipCap(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }
            expect(await Policy.minGasTipCap()).to.eq(ethers.parseEther("1"));
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setMinGasTipCap(ethers.parseEther("1"))
                ).not.to.be.reverted;
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
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).setBaseFee(0)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_BASE_FEE);
        });

        it("Should change the base fee if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setBaseFee(ethers.parseEther("1"))
                ).not.to.be.reverted;
            }
            expect(await Policy.baseFee()).to.eq(ethers.parseEther("1"));
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setBaseFee(ethers.parseEther("1"))
                ).not.to.be.reverted;
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
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).setCandidateLimit(6)
            ).to.be.revertedWithCustomError(Policy, ERRORS.INVALID_CANDIDATE_LIMIT);
        });

        it("Should change the candidate limit if meets the threshold", async function () {
            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).setCandidateLimit(2001)
                ).not.to.be.reverted;
            }
            expect(await Policy.getCandidateLimit()).to.eq(2001);
        });

        it("Should emit an event if meets the threshold", async function () {
            for (let i = 0; i < 3; i++) {
                await expect(
                    Policy.connect(signers[i]).setCandidateLimit(2001)
                ).not.to.be.reverted;
            }
            await expect(
                Policy.connect(signers[3]).setCandidateLimit(2001)
            ).emit(Policy, "SetCandidateLimit");
        });
    });

    describe("getCandidateLimit", function () {
        it("Should return default value if not setted", async function () {
            await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x4", ethers.toBeHex(0, 32)]);
            expect(await Policy.getCandidateLimit()).to.eq(CANDIDATE_LIMIT);
        });
    });
});
