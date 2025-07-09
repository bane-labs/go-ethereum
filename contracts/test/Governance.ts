import { expect } from "chai";
import { ERRORS } from "./helpers/errors.js";
import { SYS_SETTINGS, ethers, networkHelpers, provider, allocGenesis } from "./helpers/setup.js";

// MOCK PUBKEYS
const PUBKEY = "0x04a8c8762d32477f5bd0ccff58d35a7b7ace2fbbd0c0d61874bd405bc0af415690d16f585bcec5f51d1fdddfd0d4543cb0a9d40f0447b62a7c4b1a0f24c45ccb01";

describe("Governance", function () {

    let Governance: any, GovReward: any, Policy: any, KeyManagement: any;
    let MockSysCall: any;
    let signers: any;

    beforeEach(async function () {
        signers = await ethers.getSigners();
        [Governance, GovReward, Policy, KeyManagement] = await allocGenesis();

        // Deploy Mock SYS_CALL
        const deploy_mock = await ethers.deployContract("MockSysCall");
        const code_mock = await ethers.provider.send("eth_getCode", [deploy_mock.target]);
        await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.SYS_CALL, code_mock]);
        MockSysCall = await ethers.getContractAt("MockSysCall", SYS_SETTINGS.SYS_CALL, signers[0]);
    });

    describe("genesis", function () {
        it("Should get consensus size as expected", async function () {
            expect(await Governance.consensusSize()).to.eq(SYS_SETTINGS.CONSENSUS_SIZE);
        });
        it("Should get minimum vote amount as expected", async function () {
            expect(await Governance.minVoteAmount()).to.eq(SYS_SETTINGS.MIN_VOTE_AMOUNT);
        });
        it("Should get target vote amount as expected", async function () {
            expect(await Governance.voteTargetAmount()).to.eq(SYS_SETTINGS.VOTE_TARGET_AMOUNT);
        });
        it("Should get register fee as expected", async function () {
            expect(await Governance.registerFee()).to.eq(SYS_SETTINGS.REGISTER_FEE);
        });
        it("Should get epoch duration as expected", async function () {
            expect(await Governance.epochDuration()).to.eq(SYS_SETTINGS.EPOCH_DURATION);
        });
        it("Should get initial consensus as expected", async function () {
            expect(await Governance.currentConsensus(0)).to.eq(signers[0].address);
            expect(await Governance.currentConsensus(1)).to.eq(signers[1].address);
            expect(await Governance.currentConsensus(2)).to.eq(signers[2].address);
            expect(await Governance.currentConsensus(3)).to.eq(signers[3].address);
            expect(await Governance.currentConsensus(4)).to.eq(signers[4].address);
            expect(await Governance.currentConsensus(5)).to.eq(signers[5].address);
            expect(await Governance.currentConsensus(6)).to.eq(signers[6].address);
        });
        it("Should get standby validators as expected", async function () {
            expect((await Governance.standByValidators(0)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[0]);
            expect((await Governance.standByValidators(1)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[1]);
            expect((await Governance.standByValidators(2)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[2]);
            expect((await Governance.standByValidators(3)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[3]);
            expect((await Governance.standByValidators(4)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[4]);
            expect((await Governance.standByValidators(5)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[5]);
            expect((await Governance.standByValidators(6)).toLowerCase()).to.eq(SYS_SETTINGS.STANDBY_VALIDATORS[6]);
        });
    });

    describe("registerCandidate", function () {
        it("Should revert if sender is not an EOA account", async function () {
            const contract = await ethers.deployContract("MockContract");

            await expect(
                contract.call_registerCandidate(Governance, 500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.ONLY_EOA);
        });

        it("Should revert if the sender is already a candidate", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_EXISTS);
        });

        it("Should revert if the value sent is less than the registration fee", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE - BigInt(1) })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INSUFFICIENT_VALUE);
        });

        it("Should revert if register a candidate with more than 1000 shareRate", async function () {
            await expect(
                Governance.registerCandidate(1001, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INVALID_SHARE_RATE);
        });

        it("Should revert if the message key is not valid", async function () {
            await expect(
                Governance.registerCandidate(500, "0x00", { value: SYS_SETTINGS.REGISTER_FEE })
            ).to.be.revertedWithCustomError(KeyManagement, ERRORS.INVALID_MESSAGE_KEY);
        });

        it("Should register a new candidate if all conditions are met", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(1);
            expect(candidates[0]).to.equal(signers[0].address);
            expect(await KeyManagement.messagePubkeys(signers[0].address)).to.equal(PUBKEY);
        });

        it("Should emit an event when a new candidate is registered", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).emit(Governance, "Activate");
        });
    });

    describe("exitCandidate", function () {
        it("Should revert if the sender is not a candidate", async function () {
            await expect(
                Governance.exitCandidate()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should remove a candidate if all conditions are met", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(0);
        });

        it("Should decrease total votes if exited candidates received votes", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            expect(await Governance.totalVotes()).to.equal(0);
        });

        it("Should emit an event when a candidate exits", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.exitCandidate()
            ).emit(Governance, "Deactivate");
        });
    });

    describe("withdrawRegisterFee", function () {
        it("Should revert if the sender is not a candidate", async function () {
            await expect(
                Governance.withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should revert if the sender has not exited", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should revert if the sender has not waited for 2 epochs", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.withdrawRegisterFee()
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_WITHDRAW_NOT_ALLOWED);
        });

        it("Should transfer back register fee if all conditions are met", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            await networkHelpers.mine(2 * SYS_SETTINGS.EPOCH_DURATION);

            await expect(
                await Governance.withdrawRegisterFee()
            ).to.changeEtherBalance(provider, signers[0], SYS_SETTINGS.REGISTER_FEE * 50n / 100n);
        });

        it("Should emit an event when a candidate withdraw register fee", async function () {
            await expect(
                Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            await networkHelpers.mine(2 * SYS_SETTINGS.EPOCH_DURATION);

            await expect(
                Governance.withdrawRegisterFee()
            ).emit(Governance, "CandidateWithdraw");
        });
    });

    describe("deactivateCandidate", function () {
        it("Should revert if caller is not Policy", async function () {
            await expect(Governance.deactivateCandidate(signers[0])).to.be.revertedWithCustomError(
                Governance,
                ERRORS.SIDE_CALL_OT_ALLOWED
            );
        });

        it("Should update storage if a candidate is deactivated", async function () {
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.reverted(ethers);
            }

            expect(await Governance.blacklistedCandidates()).to.equal(1);
            expect(await Governance.exitHeightOf(signers[0])).to.gt(0);
            expect(await Governance.shareRateOf(signers[0])).to.equal(500);
            expect(await Governance.receivedVotes(signers[0])).to.equal(SYS_SETTINGS.VOTE_TARGET_AMOUNT);
            expect(await Governance.totalVotes()).to.equal(BigInt(SYS_SETTINGS.CONSENSUS_SIZE - 1) * SYS_SETTINGS.VOTE_TARGET_AMOUNT);
        });
    });

    describe("activateCandidate", function () {
        it("Should revert if caller is not Policy", async function () {
            await expect(Governance.activateCandidate(signers[0])).to.be.revertedWithCustomError(
                Governance,
                ERRORS.SIDE_CALL_OT_ALLOWED
            );
        });

        it("Should update storage if a candidate is activated", async function () {
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).addBlackList(signers[0])
                ).not.to.be.reverted(ethers);
            }

            for (let i = 0; i < 4; i++) {
                await expect(
                    Policy.connect(signers[i]).removeBlackList(signers[0])
                ).not.to.be.reverted(ethers);
            }

            expect(await Governance.blacklistedCandidates()).to.equal(0);
            expect(await Governance.exitHeightOf(signers[0])).to.equal(0);
            expect(await Governance.shareRateOf(signers[0])).to.equal(500);
            expect(await Governance.receivedVotes(signers[0])).to.equal(SYS_SETTINGS.VOTE_TARGET_AMOUNT);
            expect(await Governance.totalVotes()).to.equal(BigInt(SYS_SETTINGS.CONSENSUS_SIZE) * SYS_SETTINGS.VOTE_TARGET_AMOUNT);
        });
    });

    describe("getCandidates", function () {
        it("Should return an empty list of candidates initially", async function () {
            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(0);
        });

        it("Should return list of registered candidates", async function () {
            // Register some candidates
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(700, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(2);
            expect(candidates).to.include(signers[0].address);
            expect(candidates).to.include(signers[1].address);
        });

        it("Should return the updated list of candidates after a candidate exits", async function () {
            // Register some candidates
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(700, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });

            // Exit of a candidate
            await Governance.exitCandidate();

            const candidates = await Governance.getCandidates();
            expect(candidates.length).to.equal(1);
            expect(candidates).to.not.include(signers[0].address);
            expect(candidates).to.include(signers[1].address);
        });
    });

    describe("getCurrentConsensus", function () {
        it("Should return current consensus as expected", async function () {
            const consensus = await Governance.getCurrentConsensus();
            expect(consensus[0]).to.eq(signers[0].address);
            expect(consensus[1]).to.eq(signers[1].address);
            expect(consensus[2]).to.eq(signers[2].address);
            expect(consensus[3]).to.eq(signers[3].address);
            expect(consensus[4]).to.eq(signers[4].address);
            expect(consensus[5]).to.eq(signers[5].address);
            expect(consensus[6]).to.eq(signers[6].address);
        });
    });

    describe("onPersist", function () {
        it("Should revert if caller is not SYS_CALL", async function () {
            await expect(Governance.onPersist()).to.be.revertedWithCustomError(
                Governance,
                ERRORS.SIDE_CALL_OT_ALLOWED
            );
        });

        it("Should allow SYS_CALL", async function () {
            expect(await MockSysCall.call_onPersist(Governance)).not.to.be.reverted(ethers);
        });

        it("Should update currentEpochStartHeight on Epoch change", async function () {
            expect(await Governance.currentEpochStartHeight()).to.equal(0);
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await expect(
                MockSysCall.call_onPersist(Governance)
            ).emit(Governance, "Persist");
            expect(await Governance.currentEpochStartHeight()).to.equal(
                await ethers.provider.getBlockNumber()
            );
        });

        it("Should take standby validators as consensus if vote amount not meets threshold", async function () {
            // Register without voting
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.deep.equal(SYS_SETTINGS.STANDBY_VALIDATORS);
        });

        it("Should take standby validators as consensus if candidate amount not meets threshold", async function () {
            // Register only 1 candidate but vote
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.vote(signers[0], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.deep.equal(SYS_SETTINGS.STANDBY_VALIDATORS);
        });

        it("Should change consensus if all condition meets", async function () {
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            expect(await Governance.getCurrentConsensus()).to.not.deep.equal(SYS_SETTINGS.STANDBY_VALIDATORS);
        });

        it("Should distribute correct rewards to consensus", async function () {
            const tx = await signers[0].sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await expect(
                await MockSysCall.call_onPersist(Governance)
            ).to.changeEtherBalance(provider, signers[0], 1000000000000000000n);
        });
    });

    describe("vote", function () {
        it("Should revert if vote value is insufficient", async function () {
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT - 1n })
            ).to.be.revertedWithCustomError(Governance, ERRORS.INSUFFICIENT_VALUE);
        });

        it("Should revert if target is not a candidate", async function () {
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should revert if the sender has voted to another candidate", async function () {
            // Register some candidates
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(700, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });

            // Vote to the first
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            // Vote to the second
            await expect(
                Governance.vote(signers[1], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).to.be.revertedWithCustomError(Governance, ERRORS.MULTIPLE_VOTE_NOT_ALLOWED);
        });

        it("Should update vote data if all conditions are met", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            expect(await Governance.receivedVotes(signers[0])).to.eq(SYS_SETTINGS.MIN_VOTE_AMOUNT);
            expect(await Governance.votedTo(signers[0])).to.eq(signers[0].address);
            expect(await Governance.votedAmount(signers[0])).to.eq(SYS_SETTINGS.MIN_VOTE_AMOUNT);
        });

        it("Should emit an event when a voter votes", async function () {
            // Register some candidates
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).emit(Governance, "Vote");
        });
    });

    describe("revokeVote", function () {
        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.revokeVote(0)
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should transfer back GAS if all conditions are met", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });

            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            const balanceBefore = await ethers.provider.getBalance(signers[0]);
            await expect(
                Governance.revokeVote(SYS_SETTINGS.MIN_VOTE_AMOUNT)
            ).not.to.be.reverted(ethers);
            const balanceAfter = await ethers.provider.getBalance(signers[0]);
            expect(balanceAfter).to.gt(balanceBefore);
            expect(balanceAfter).to.lt(balanceBefore + SYS_SETTINGS.MIN_VOTE_AMOUNT);
        });

        it("Should update vote data if all conditions are met", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.revokeVote(SYS_SETTINGS.MIN_VOTE_AMOUNT)
            ).not.to.be.reverted(ethers);

            expect(await Governance.receivedVotes(signers[0])).to.eq(0);
            expect(await Governance.votedTo(signers[0])).to.eq(ethers.ZeroAddress);
            expect(await Governance.votedAmount(signers[0])).to.eq(0);
        });

        it("Should emit an event when a voter revokes", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.revokeVote(SYS_SETTINGS.MIN_VOTE_AMOUNT)
            ).emit(Governance, "Revoke");
        });
    });

    describe("transferVote", function () {
        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.transferVote(signers[0])
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should revert if the candidate from and to is the same", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.transferVote(signers[0])
            ).to.be.revertedWithCustomError(Governance, ERRORS.SAME_CANDIDATE);
        });

        it("Should revert if the candidate to is not in the candidate list", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.transferVote(signers[1])
            ).to.be.revertedWithCustomError(Governance, ERRORS.CANDIDATE_NOT_EXISTS);
        });

        it("Should update votes if all conditions are met", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.transferVote(signers[1])
            ).not.to.be.reverted(ethers);

            expect(await Governance.receivedVotes(signers[0])).to.eq(0);
            expect(await Governance.receivedVotes(signers[1])).to.eq(SYS_SETTINGS.MIN_VOTE_AMOUNT);
            expect(await Governance.votedTo(signers[0])).to.eq(signers[1].address);
            expect(await Governance.votedAmount(signers[0])).to.eq(SYS_SETTINGS.MIN_VOTE_AMOUNT);
        });

        it("Should update total votes if transfer from a deactivated candidate", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);
            await expect(
                Governance.exitCandidate()
            ).not.to.be.reverted(ethers);

            expect(await Governance.totalVotes()).to.equal(0);
            await expect(
                Governance.transferVote(signers[1])
            ).not.to.be.reverted(ethers);
            expect(await Governance.totalVotes()).to.equal(SYS_SETTINGS.MIN_VOTE_AMOUNT);
        });

        it("Should emit two events when a voter transfer votes", async function () {
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await expect(
                Governance.vote(signers[0], { value: SYS_SETTINGS.MIN_VOTE_AMOUNT })
            ).not.to.be.reverted(ethers);

            await expect(
                Governance.transferVote(signers[1])
            ).emit(Governance, "Revoke").emit(Governance, "Vote");
        });
    });

    describe("claimReward", function () {
        it("Should revert if the sender has not voted", async function () {
            await expect(
                Governance.claimReward()
            ).to.be.revertedWithCustomError(Governance, ERRORS.NO_VOTE);
        });

        it("Should update reward data if all conditions are met", async function () {
            // Register enough candidates and change consensus
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await signers[0].sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await Governance.claimReward()
            expect(
                await Governance.voterGasPerVote(signers[0])
            ).to.eq(166666666666666);
        });

        it("Should work with 1000 share rate and 0 votes", async function () {
            // Register enough candidates and change consensus
            await Governance.registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            await Governance.connect(signers[1]).registerCandidate(1000, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            for (let i = 2; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
            }
            for (let i = 1; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await signers[0].sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await expect(
                MockSysCall.call_onPersist(Governance)
            ).to.be.not.reverted(ethers);
        });

        it("Should transfer back correct reward if all conditions are met", async function () {
            // Register enough candidates and change consensus
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await signers[0].sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await expect(
                await Governance.claimReward()
            ).to.changeEtherBalance(provider, signers[0], 499999999999998000n);
        });

        it("Should emit an event when a voter claims", async function () {
            // Register enough candidates and change consensus
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await signers[0].sendTransaction({
                to: GovReward.target,
                value: ethers.parseEther("7"),
            });
            await tx.wait();
            await MockSysCall.call_onPersist(Governance);

            await expect(
                await Governance.claimReward()
            ).emit(Governance, "VoterClaim");
        });
    });

    describe("unclaimedRewardOf", function () {
        it("Should return correct reward amount", async function () {
            // Register enough candidates and change consensus
            for (let i = 0; i < SYS_SETTINGS.CONSENSUS_SIZE; i++) {
                await Governance.connect(signers[i]).registerCandidate(500, PUBKEY, { value: SYS_SETTINGS.REGISTER_FEE });
                await Governance.connect(signers[i]).vote(signers[i], { value: SYS_SETTINGS.VOTE_TARGET_AMOUNT });
            }
            await networkHelpers.mine(SYS_SETTINGS.EPOCH_DURATION);
            await MockSysCall.call_onPersist(Governance);

            // Send GAS as governance reward and persist it
            const tx = await signers[0].sendTransaction({
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
