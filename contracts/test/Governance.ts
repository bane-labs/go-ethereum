import { ethers } from "hardhat";
import { expect } from "chai";
import { mine } from "@nomicfoundation/hardhat-network-helpers";
import { ERRORS } from "./helpers/errors";

// NATIVE ADDRESSES
const GOV_ADMIN = "0x1212000000000000000000000000000000000000";
const GOV_PROXY = "0x1212000000000000000000000000000000000001";
const GOV_IMP = "0x1212100000000000000000000000000000000001";
const POLICY_PROXY = "0x1212000000000000000000000000000000000002";
const POLICY_IMP = "0x1212100000000000000000000000000000000002";
const REWARD_PROXY = "0x1212000000000000000000000000000000000003";
const REWARD_IMP = "0x1212100000000000000000000000000000000003";
const SYS_CALL = "0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE";

// CONFIG
const CONSENSUS_SIZE = 7;
const MIN_VOTE_AMOUNT = ethers.parseEther("1");
const VOTE_TARGET_AMOUNT = ethers.parseEther("3000");
const REGISTER_FEE = ethers.parseEther("1000");
const EPOCH_DURATION = 60480;
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

describe("Governance", function () {

    let Governance: any;
    let user: any, candidate1: any, candidate2: any;

    beforeEach(async function () {
        // Signers
        [user, candidate1, candidate2] = await ethers.getSigners();

        // Reset blockchain state
        await ethers.provider.send("hardhat_reset")

        // Deploy Governance contract
        const governance_deploy = await ethers.deployContract("Governance");
        const reward_deploy = await ethers.deployContract("GovReward");
        const policy_deploy = await ethers.deployContract("Policy");

        // Copy Bytecode to native address
        const governance_code = await ethers.provider.send("eth_getCode", [governance_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [GOV_PROXY, governance_code]);

        const reward_code = await ethers.provider.send("eth_getCode", [reward_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, reward_code]);

        const policy_code = await ethers.provider.send("eth_getCode", [policy_deploy.target]);
        await ethers.provider.send("hardhat_setCode", [POLICY_PROXY, policy_code]);
        const contract = require("../artifacts/solidity/Governance.sol/Governance.json");
        Governance = new ethers.Contract(GOV_PROXY, contract.abi, user);

        // Write Governance config to storage
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x2", ethers.toBeHex(MIN_VOTE_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x3", ethers.toBeHex(VOTE_TARGET_AMOUNT, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x4", ethers.toBeHex(REGISTER_FEE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x5", ethers.toBeHex(EPOCH_DURATION, 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x10", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae672", ethers.toBeHex(STANDBY_VALIDATORS[0], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae673", ethers.toBeHex(STANDBY_VALIDATORS[1], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae674", ethers.toBeHex(STANDBY_VALIDATORS[2], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae675", ethers.toBeHex(STANDBY_VALIDATORS[3], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae676", ethers.toBeHex(STANDBY_VALIDATORS[4], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae677", ethers.toBeHex(STANDBY_VALIDATORS[5], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae678", ethers.toBeHex(STANDBY_VALIDATORS[6], 32)]);

        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x11", ethers.toBeHex(CONSENSUS_SIZE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c68", ethers.toBeHex(STANDBY_VALIDATORS[0], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c69", ethers.toBeHex(STANDBY_VALIDATORS[1], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6a", ethers.toBeHex(STANDBY_VALIDATORS[2], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6b", ethers.toBeHex(STANDBY_VALIDATORS[3], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6c", ethers.toBeHex(STANDBY_VALIDATORS[4], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6d", ethers.toBeHex(STANDBY_VALIDATORS[5], 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6e", ethers.toBeHex(STANDBY_VALIDATORS[6], 32)]);
      
        // Write Policy config to storage
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x2", ethers.toBeHex(MIN_GAS_TIP_CAP, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x3", ethers.toBeHex(BASE_FEE, 32)]);
        await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x4", ethers.toBeHex(CANDIDATE_LIMIT, 32)]);
    });

    describe("genesis", function () {
        it("Should get consensus size as expected", async function () {
            expect(await Governance.consensusSize()).to.eq(CONSENSUS_SIZE);
        });
        it("Should get minimum vote amount as expected", async function () {
            expect(await Governance.minVoteAmount()).to.eq(MIN_VOTE_AMOUNT);
        });
        it("Should get target vote amount as expected", async function () {
            expect(await Governance.voteTargetAmount()).to.eq(VOTE_TARGET_AMOUNT);
        });
        it("Should get register fee as expected", async function () {
            expect(await Governance.registerFee()).to.eq(REGISTER_FEE);
        });
        it("Should get epoch duration as expected", async function () {
            expect(await Governance.epochDuration()).to.eq(EPOCH_DURATION);
        });
        it("Should get initial consensus as expected", async function () {
            expect((await Governance.currentConsensus(0)).toLowerCase()).to.eq(STANDBY_VALIDATORS[0]);
            expect((await Governance.currentConsensus(1)).toLowerCase()).to.eq(STANDBY_VALIDATORS[1]);
            expect((await Governance.currentConsensus(2)).toLowerCase()).to.eq(STANDBY_VALIDATORS[2]);
            expect((await Governance.currentConsensus(3)).toLowerCase()).to.eq(STANDBY_VALIDATORS[3]);
            expect((await Governance.currentConsensus(4)).toLowerCase()).to.eq(STANDBY_VALIDATORS[4]);
            expect((await Governance.currentConsensus(5)).toLowerCase()).to.eq(STANDBY_VALIDATORS[5]);
            expect((await Governance.currentConsensus(6)).toLowerCase()).to.eq(STANDBY_VALIDATORS[6]);
        });
        it("Should get standby validators as expected", async function () {
            expect((await Governance.standByValidators(0)).toLowerCase()).to.eq(STANDBY_VALIDATORS[0]);
            expect((await Governance.standByValidators(1)).toLowerCase()).to.eq(STANDBY_VALIDATORS[1]);
            expect((await Governance.standByValidators(2)).toLowerCase()).to.eq(STANDBY_VALIDATORS[2]);
            expect((await Governance.standByValidators(3)).toLowerCase()).to.eq(STANDBY_VALIDATORS[3]);
            expect((await Governance.standByValidators(4)).toLowerCase()).to.eq(STANDBY_VALIDATORS[4]);
            expect((await Governance.standByValidators(5)).toLowerCase()).to.eq(STANDBY_VALIDATORS[5]);
            expect((await Governance.standByValidators(6)).toLowerCase()).to.eq(STANDBY_VALIDATORS[6]);
        });
    });

    describe("registerCandidate", function () {
        it("Should revert if sender is not an EOA account", async function () {
            const contract = await ethers.deployContract("MockContract");

            await expect(
                contract.call_registerCandidate(Governance, 500, { value: REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.ONLY_EOA);
        });

        it("Should revert if the sender is already a candidate", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });

            await expect(
                Governance
                    .connect(candidate1)
                    .registerCandidate(500, { value: REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_EXISTS);
        });

        it("Should revert if the value sent is less than the registration fee", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE - BigInt(1) })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INSUFFICIENT_VALUE);
        });

        it("Should revert if register a candidate with more than 1000 shareRate", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(1001, { value: REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INVALID_SHARE_RATE);
        });

        // it("Should revert if the candidate amount exceeds limit", async function () {
        //     await ethers.provider.send("hardhat_setStorageAt", [POLICY_PROXY, "0x4", ethers.toBeHex(0, 32)]);
        //     await expect(
        //         Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
        //     ).to.be.revertedWithCustomError(Governance, ERRORS.REGISTER_DISABLED);
        // });

        it("Should register a new candidate if all conditions are met", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(1);
            expect(candidates[0]).to.equal(candidate1.address);
        });

        it("Should emit an event when a new candidate is registered", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).emit(Governance, "Register");
        });
    });

    describe("exitCandidate", function () {
        it("Should revert if the sender is not a candidate", async function () {
            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should remove a candidate if all conditions are met", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).not.to.be.reverted;

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(0);
        });

        it("Should emit an event when a candidate exits", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).emit(Governance, "Exit");
        });
    });

    describe("withdrawRegisterFee", function () {
        it("Should revert if the sender is not a candidate", async function () {
            await expect(
                Governance.connect(candidate1).withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should revert if the sender has not exited", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should revert if the sender has not waited for 2 epochs", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;
            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should transfer back register fee if all conditions are met", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;
            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).not.to.be.reverted;

            await mine(2 * EPOCH_DURATION);

            await expect(
                await Governance.connect(candidate1).withdrawRegisterFee()
            ).to.changeEtherBalance(candidate1, REGISTER_FEE);
        });

        it("Should emit an event when a candidate withdraw register fee", async function () {
            await expect(
                Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE })
            ).not.to.be.reverted;
            await expect(
                Governance.connect(candidate1).exitCandidate()
            ).not.to.be.reverted;

            await mine(2 * EPOCH_DURATION);

            await expect(
                Governance.connect(candidate1).withdrawRegisterFee()
            ).emit(Governance, "CandidateWithdraw");
        });
    });

    describe("getCandidates", function () {
        it("Should return an empty list of candidates initially", async function () {
            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(0);
        });

        it("Should return list of registered candidates", async function () {
            // Register some candidates
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(700, { value: REGISTER_FEE });

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(2);
            expect(candidates).to.include(candidate1.address);
            expect(candidates).to.include(candidate2.address);
        });

        it("Should return the updated list of candidates after a candidate exits", async function () {
            // Register some candidates
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(700, { value: REGISTER_FEE });

            // Exit of a candidate
            await Governance.connect(candidate1).exitCandidate();

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(1);
            expect(candidates).to.not.include(candidate1.address);
            expect(candidates).to.include(candidate2.address);
        });
    });

    describe("getCurrentConsensus", function () {
        it("Should return standByValidators initially", async function () {
            expect(await Governance.getCurrentConsensus()).to.deep.equal(STANDBY_VALIDATORS);
        });
    });

    describe("onPersist", function () {
        let MockSysCall: any;
        let GovReward: any;

        beforeEach(async function () {
            // Deploy Mock SYS_CALL
            const deploy_mock = await ethers.deployContract("MockSysCall");
            const code_mock = await ethers.provider.send("eth_getCode", [deploy_mock.target]);
            await ethers.provider.send("hardhat_setCode", [SYS_CALL, code_mock]);
            const contract_mock = require("../artifacts/solidity/test/MockSysCall.sol/MockSysCall.json");
            MockSysCall = new ethers.Contract(SYS_CALL, contract_mock.abi, user);
            // Deploy GovReward to native address
            const deploy_reward = await ethers.deployContract("GovReward");
            const code_reward = await ethers.provider.send("eth_getCode", [deploy_reward.target]);
            await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, code_reward]);
            const contract_reward = require("../artifacts/solidity/GovReward.sol/GovReward.json");
            GovReward = new ethers.Contract(REWARD_PROXY, contract_reward.abi, user);
        });

        it("Should revert if caller is not SYS_CALL", async function () {
            await expect(Governance.connect(user).onPersist()).to.be.revertedWithCustomError(
                Governance,
                ERRORS.SIDE_CALL_OT_ALLOWED
            );
        });

        it("Should allow SYS_CALL", async function () {
            expect(await MockSysCall.call_onPersist(Governance)).not.to.be.reverted;
        });

        it("Should update currentEpochStartHeight on Epoch change", async function () {
            expect(await Governance.currentEpochStartHeight()).to.equal(0);
            await mine(EPOCH_DURATION);
            await expect(
                MockSysCall.call_onPersist(Governance)
            ).emit(Governance, "Persist");
            expect(await Governance.currentEpochStartHeight()).to.equal(
                await ethers.provider.getBlockNumber()
            );
        });

        it("Should take standby validators as consensus if vote amount not meets threshold", async function () {
            // Register without voting
            let signers = await ethers.getSigners();
            for (let i = 0; i < signers.length; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.deep.equal(STANDBY_VALIDATORS);
        });

        it("Should take standby validators as consensus if candidate amount not meets threshold", async function () {
            // Register only 1 candidate but vote
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate1).vote(candidate1, { value: VOTE_TARGET_AMOUNT });
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.deep.equal(STANDBY_VALIDATORS);
        });

        it("Should change consensus if all condition meets", async function () {
            let signers = await ethers.getSigners();
            for (let i = 0; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.not.deep.equal(STANDBY_VALIDATORS);
        });

        it("Should distribute correct rewards to consensus", async function () {
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await expect(
                await MockSysCall.call_onPersist(Governance)
            ).to.changeEtherBalance(STANDBY_VALIDATORS[0], 1000000000000000000n);
        });
    });

    describe("vote", function () {
        it("Should revert if vote value is insufficient", async function () {
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT - 1n })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INSUFFICIENT_VALUE);
        });

        it("Should revert if target is not a candidate", async function () {
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT})
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should revert if the sender has voted to another candidate", async function () {
            // Register some candidates
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(700, { value: REGISTER_FEE });

            // Vote to the first
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            // Vote to the second
            await expect(
                Governance.connect(candidate1).vote(candidate2, { value: MIN_VOTE_AMOUNT })
            ).to.be.revertedWithCustomError(Governance, ERRORS.MULTIPLE_VOTE_NOT_ALLOWED);
        });

        it("Should update vote data if all conditions are met", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });

            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            expect(await Governance.receivedVotes(candidate1.address)).to.eq(MIN_VOTE_AMOUNT);
            expect(await Governance.votedTo(candidate1.address)).to.eq(candidate1.address);
            expect(await Governance.votedAmount(candidate1.address)).to.eq(MIN_VOTE_AMOUNT);
            expect(await Governance.voteHeight(candidate1.address)).to.eq(
                await ethers.provider.getBlockNumber()
            );
        });

        it("Should emit an event when a voter votes", async function () {
            // Register some candidates
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(700, { value: REGISTER_FEE });

            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).emit(Governance, "Vote");
        });
    });

    describe("revokeVote", function () {
        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.connect(candidate1).revokeVote()
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should transfer back GAS if all conditions are met", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });

            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            const balanceBefore = await ethers.provider.getBalance(candidate1.address);
            await expect(
                Governance.connect(candidate1).revokeVote()
            ).not.to.be.reverted;
            const balanceAfter = await ethers.provider.getBalance(candidate1.address);
            expect(balanceAfter).to.gt(balanceBefore);
            expect(balanceAfter).to.lt(balanceBefore + MIN_VOTE_AMOUNT);
        });

        it("Should update vote data if all conditions are met", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;
            await expect(
                Governance.connect(candidate1).revokeVote()
            ).not.to.be.reverted;

            expect(await Governance.receivedVotes(candidate1.address)).to.eq(0);
            expect(await Governance.votedTo(candidate1.address)).to.eq(ethers.ZeroAddress);
            expect(await Governance.votedAmount(candidate1.address)).to.eq(0);
            expect(await Governance.voteHeight(candidate1.address)).to.eq(0);
        });

        it("Should emit an event when a voter revokes", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;
            await expect(
                Governance.connect(candidate1).revokeVote()
            ).emit(Governance, "Revoke");
        });
    });

    describe("transferVote", function () {
        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.connect(candidate1).transferVote(candidate1)
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should revert if the candidate from and to is the same", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).transferVote(candidate1)
            ).to.be.revertedWithCustomError(Governance, ERRORS.SAME_CANDIDATE);
        });

        it("Should revert if the candidate to is not in the candidate list", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).transferVote(candidate2)
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should update votes if all conditions are met", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).transferVote(candidate2)
            ).not.to.be.reverted;

            expect(await Governance.receivedVotes(candidate1.address)).to.eq(0);
            expect(await Governance.receivedVotes(candidate2.address)).to.eq(MIN_VOTE_AMOUNT);
            expect(await Governance.votedTo(candidate1.address)).to.eq(candidate2.address);
            expect(await Governance.votedAmount(candidate1.address)).to.eq(MIN_VOTE_AMOUNT);
            expect(await Governance.voteHeight(candidate1.address)).to.eq(await ethers.provider.getBlockNumber());
        });

        it("Should emit two events when a voter transfer votes", async function () {
            await Governance.connect(candidate1).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(candidate2).registerCandidate(500, { value: REGISTER_FEE });
            await expect(
                Governance.connect(candidate1).vote(candidate1, { value: MIN_VOTE_AMOUNT })
            ).not.to.be.reverted;

            await expect(
                Governance.connect(candidate1).transferVote(candidate2)
            ).emit(Governance, "Revoke").emit(Governance, "Vote");
        });
    });

    describe("claimReward", function () {
        let MockSysCall: any;
        let GovReward: any;

        beforeEach(async function () {
            // Deploy Mock SYS_CALL
            const deploy_mock = await ethers.deployContract("MockSysCall");
            const code_mock = await ethers.provider.send("eth_getCode", [deploy_mock.target]);
            await ethers.provider.send("hardhat_setCode", [SYS_CALL, code_mock]);
            const contract_mock = require("../artifacts/solidity/test/MockSysCall.sol/MockSysCall.json");
            MockSysCall = new ethers.Contract(SYS_CALL, contract_mock.abi, user);
            // Deploy GovReward to native address
            const deploy_reward = await ethers.deployContract("GovReward");
            const code_reward = await ethers.provider.send("eth_getCode", [deploy_reward.target]);
            await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, code_reward]);
            const contract_reward = require("../artifacts/solidity/GovReward.sol/GovReward.json");
            GovReward = new ethers.Contract(REWARD_PROXY, contract_reward.abi, user);
        });

        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.connect(candidate1).claimReward()
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should update reward data if all conditions are met", async function () {
            // Register enough candidates and change consensus
            let signers = await ethers.getSigners();
            for (let i = 0; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await Governance.connect(signers[0]).claimReward()
            expect(
                await Governance.voterGasPerVote(signers[0])
            ).to.eq(166666666666666);
        });

        it("Should work with 1000 share rate and 0 votes", async function () {
            // Register enough candidates and change consensus
            let signers = await ethers.getSigners();
            await Governance.connect(signers[0]).registerCandidate(500, { value: REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(1000, { value: REGISTER_FEE });
            for (let i = 2; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
            }
            for (let i = 1; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await expect(
                MockSysCall.call_onPersist(Governance)
            ).to.be.not.reverted;
        });

        it("Should transfer back correct reward if all conditions are met", async function () {
            // Register enough candidates and change consensus
            let signers = await ethers.getSigners();
            for (let i = 0; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await expect(
                await Governance.connect(signers[0]).claimReward()
            ).to.changeEtherBalance(signers[0], 499999999999998000n);
        });

        it("Should emit an event when a voter claims", async function () {
            // Register enough candidates and change consensus
            let signers = await ethers.getSigners();
            for (let i = 0; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await expect(
                await Governance.connect(signers[0]).claimReward()
            ).emit(Governance, "VoterClaim");
        });
    });

    describe("unclaimedRewardOf", function () {
        let MockSysCall: any;
        let GovReward: any;

        beforeEach(async function () {
            // Deploy Mock SYS_CALL
            const deploy_mock = await ethers.deployContract("MockSysCall");
            const code_mock = await ethers.provider.send("eth_getCode", [deploy_mock.target]);
            await ethers.provider.send("hardhat_setCode", [SYS_CALL, code_mock]);
            const contract_mock = require("../artifacts/solidity/test/MockSysCall.sol/MockSysCall.json");
            MockSysCall = new ethers.Contract(SYS_CALL, contract_mock.abi, user);
            // Deploy GovReward to native address
            const deploy_reward = await ethers.deployContract("GovReward");
            const code_reward = await ethers.provider.send("eth_getCode", [deploy_reward.target]);
            await ethers.provider.send("hardhat_setCode", [REWARD_PROXY, code_reward]);
            const contract_reward = require("../artifacts/solidity/GovReward.sol/GovReward.json");
            GovReward = new ethers.Contract(REWARD_PROXY, contract_reward.abi, user);
        });

        it("Should return correct reward amount", async function () {
            // Register enough candidates and change consensus
            let signers = await ethers.getSigners();
            for (let i = 0; i < CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, { value: REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: VOTE_TARGET_AMOUNT });
            }
            await mine(EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await user.sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            expect(
                await Governance.unclaimedRewardOf(signers[0])
            ).to.eq(499999999999998000n);
        });
    });
});
