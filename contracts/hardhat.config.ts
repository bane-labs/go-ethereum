import type { HardhatUserConfig } from "hardhat/config";

import hardhatToolboxMochaEthersPlugin from "@nomicfoundation/hardhat-toolbox-mocha-ethers";

const config: HardhatUserConfig = {
  /*
   * In Hardhat 3, plugins are defined as part of the Hardhat config instead of
   * being based on the side-effect of imports.
   *
   * Note: A `hardhat-toolbox` like plugin for Hardhat 3 hasn't been defined yet,
   * so this list is larger than what you would normally have.
   */
  plugins: [hardhatToolboxMochaEthersPlugin],
  solidity: {
    profiles: {
      default: {
        version: "0.8.25",
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
  paths: {
    sources: "./solidity",
    tests: "./test",
  }
};

export default config;
