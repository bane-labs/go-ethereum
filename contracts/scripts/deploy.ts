import { network } from "hardhat";

const { ethers, networkName } = await network.create();

console.log(`Deploying Governance to ${networkName}...`);

const governance = await ethers.deployContract("Governance");

console.log("Waiting for the deployment tx to confirm");
await governance.waitForDeployment();

console.log("Governance address:", await governance.getAddress());

console.log("Deployment successful!");