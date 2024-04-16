# Changelog

This document outlines major changes between releases.

## 0.1.0 Attraction (16 April 2024)

Initial long-term supported node version including dBFT consensus engine integration with
single-block state finality and two-blocks hash finality based on the v1.13.11 Geth
implementation. Also includes the set of system contracts implemented: Policy, Governance
and Bridge with related dependent contracts and relevant node integration logic. This version
mostly focuses on the P2P protocol and storage scheme stabilization, system contracts
implementation and dBFT consensus layer integration. It also includes bug fixes critical for
the node functioning and consensus liveness.

This version is aimed to be launched in Neo X T3 testnet and be supported for the whole
T3 lifetime with possible minor patches and non-breaking upgrades.  Node operators may
use genesis configuration file located at `config/genesis_testnet.json` to run
T3 node.
