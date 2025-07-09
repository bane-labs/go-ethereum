import { network } from "hardhat";

export const { ethers, networkHelpers, provider } = await network.connect();

export const SYS_SETTINGS = {
    // NATIVE ADDRESSES
    GOV_PROXY: "0x1212000000000000000000000000000000000001",
    POLICY_PROXY: "0x1212000000000000000000000000000000000002",
    REWARD_PROXY: "0x1212000000000000000000000000000000000003",
    BRIDGE_PROXY: "0x1212000000000000000000000000000000000004",
    KEYMANAGEMENT_PROXY: "0x1212000000000000000000000000000000000008",
    SYS_CALL: "0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE",

    // CONFIG
    CONSENSUS_SIZE: 7,
    MIN_VOTE_AMOUNT: ethers.parseEther("1"),
    VOTE_TARGET_AMOUNT: ethers.parseEther("3000"),
    REGISTER_FEE: ethers.parseEther("1000"),
    EPOCH_DURATION: 60480,
    SHARE_PERIOD: 180,
    STANDBY_VALIDATORS: [
        "0xcbbeca26e89011e32ba25610520b20741b809007",
        "0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc",
        "0xd10f47396dc6c76ad53546158751582d3e2683ef",
        "0xa51fe05b0183d01607bf48c1718d1168a1c11171",
        "0x01b517b301bb143476da35bb4a1399500d925514",
        "0x7976ad987d572377d39fb4bab86c80e08b6f8327",
        "0xd711da2d8c71a801fc351163337656f1321343a0"
    ],

    MIN_GAS_TIP_CAP: ethers.parseUnits("1", "gwei"),
    BASE_FEE: ethers.parseUnits("1", "gwei"),
    CANDIDATE_LIMIT: 2000,
};

export const allocGenesis = async () => {
    // Signers
    const signers = await ethers.getSigners();

    // Reset blockchain state
    await ethers.provider.send("hardhat_reset");

    // Deploy Governance contract
    const governance_deploy = await ethers.deployContract("Governance");
    const reward_deploy = await ethers.deployContract("GovReward");
    const policy_deploy = await ethers.deployContract("Policy");
    const keymanagement_deploy = await ethers.deployContract("KeyManagementV0");

    // Copy Bytecode to native address
    const governance_code = await ethers.provider.send("eth_getCode", [governance_deploy.target]);
    await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.GOV_PROXY, governance_code]);

    const reward_code = await ethers.provider.send("eth_getCode", [reward_deploy.target]);
    await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.REWARD_PROXY, reward_code]);

    const policy_code = await ethers.provider.send("eth_getCode", [policy_deploy.target]);
    await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.POLICY_PROXY, policy_code]);

    const keymanagement_code = await ethers.provider.send("eth_getCode", [keymanagement_deploy.target]);
    await ethers.provider.send("hardhat_setCode", [SYS_SETTINGS.KEYMANAGEMENT_PROXY, keymanagement_code]);

    const governance_instance = await ethers.getContractAt("Governance", SYS_SETTINGS.GOV_PROXY, signers[0]);
    const reward_instance = await ethers.getContractAt("GovReward", SYS_SETTINGS.REWARD_PROXY, signers[0]);
    const policy_instance = await ethers.getContractAt("Policy", SYS_SETTINGS.POLICY_PROXY, signers[0]);
    const keymanagement_instance = await ethers.getContractAt("KeyManagementV0", SYS_SETTINGS.KEYMANAGEMENT_PROXY, signers[0]);

    // Write Governance config to storage
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1", ethers.toBeHex(SYS_SETTINGS.CONSENSUS_SIZE, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x2", ethers.toBeHex(SYS_SETTINGS.MIN_VOTE_AMOUNT, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x3", ethers.toBeHex(SYS_SETTINGS.VOTE_TARGET_AMOUNT, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x4", ethers.toBeHex(SYS_SETTINGS.REGISTER_FEE, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x5", ethers.toBeHex(SYS_SETTINGS.EPOCH_DURATION, 32)]);

    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x10", ethers.toBeHex(SYS_SETTINGS.CONSENSUS_SIZE, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae672", ethers.toBeHex(signers[0].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae673", ethers.toBeHex(signers[1].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae674", ethers.toBeHex(signers[2].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae675", ethers.toBeHex(signers[3].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae676", ethers.toBeHex(signers[4].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae677", ethers.toBeHex(signers[5].address, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x1b6847dc741a1b0cd08d278845f9d819d87b734759afb55fe2de5cb82a9ae678", ethers.toBeHex(signers[6].address, 32)]);

    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x11", ethers.toBeHex(SYS_SETTINGS.CONSENSUS_SIZE, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c68", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[0], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c69", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[1], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6a", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[2], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6b", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[3], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6c", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[4], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6d", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[5], 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x31ecc21a745e3968a04e9570e4425bc18fa8019c68028196b546d1669c200c6e", ethers.toBeHex(SYS_SETTINGS.STANDBY_VALIDATORS[6], 32)]);

    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.GOV_PROXY, "0x17", ethers.toBeHex(SYS_SETTINGS.SHARE_PERIOD, 32)]);

    // Write Policy config to storage
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0x2", ethers.toBeHex(SYS_SETTINGS.MIN_GAS_TIP_CAP, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0x3", ethers.toBeHex(SYS_SETTINGS.BASE_FEE, 32)]);
    await ethers.provider.send("hardhat_setStorageAt", [SYS_SETTINGS.POLICY_PROXY, "0x4", ethers.toBeHex(SYS_SETTINGS.CANDIDATE_LIMIT, 32)]);

    return [governance_instance, reward_instance, policy_instance, keymanagement_instance];
};
