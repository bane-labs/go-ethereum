import { defineConfig } from "hardhat/config";
import hardhatVerify from "@nomicfoundation/hardhat-verify";

import hardhatEthers from "@nomicfoundation/hardhat-ethers";
import hardhatNetworkHelpers from "@nomicfoundation/hardhat-network-helpers";
import hardhatToolboxMochaEthersPlugin from "@nomicfoundation/hardhat-toolbox-mocha-ethers";

export default defineConfig({
  plugins: [hardhatEthers, hardhatNetworkHelpers, hardhatToolboxMochaEthersPlugin, hardhatVerify],
  solidity: {
    profiles: {
      default: {
        version: "0.8.36",
        settings: {
          evmVersion: "cancun",
          viaIR: true,
          optimizer: {
            enabled: true,
            runs: 500,
          },
        },
      },
    },
  },
  networks: {
    mainnet: {
      type: "http",
      chainId: 47763,
      url: "https://mainnet-1.rpc.banelabs.org",
      accounts: [],
      gasPrice: 40000000000,
    },
    testnet: {
      type: "http",
      chainId: 12227332,
      url: "https://neoxt4seed1.ngd.network",
      accounts: [],
      gasPrice: 40000000000,
    }
  },
  chainDescriptors: {
    47763: {
      name: "mainnet",
      blockExplorers: {
        etherscan: {
          name: "Mainnet Explorer",
          url: "https://neoxscan.ngd.network",
          apiUrl: "https://xexplorer.neo.org:8877/api",
        },
      },
    },
    12227332: {
      name: "testnet",
      blockExplorers: {
        etherscan: {
          name: "Testnet Explorer",
          url: "https://neoxt4scan.ngd.network",
          apiUrl: "https://xt4scan.ngd.network:8877/api",
        },
      },
    }
  },
  verify: {
    etherscan: {
      apiKey: "YourEtherscanApiKey",
    },
  },
  paths: {
    sources: "./solidity",
    tests: "./test",
  }
});

