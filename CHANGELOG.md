# Changelog

This document outlines major changes between releases.

## 0.2.2 "Disjunction" (21 Aug 2024)

Another v0.2.0 compatible version of Neo X node that includes Geth update to v1.13.15
and a set of bugfixes critical for both MainNet and T4 TestNet functioning.

Node operators should update the binary for both T4 and MainNet nodes, no DB resync
is needed.

Improvements:
 * base Geth source code is updated to v1.13.15 (#232)

Bug fixed:
 * panic during `PREVRANDAO` opcode handling (#289)
 * duplicated transactions requests are sending by dBFT (#292)
 * transaction receipts carrying non-canonical block hash (#232)
 * race in sealed block submission to miner (#302)
 * frequent peer disconnections due to invalid extensible dBFT payload sender (#298)

## 0.2.1 "Graphitization" (24 Jul 2024)

A patch version compatible with v0.2.0 release of Neo X node that brings a set of
minor compatible improvements and bug fixes to system contracts, dBFT consensus
engine and RPC APIs. This version is aimed to be run on the initial version of Neo X
MainNet.

A new `config/genesis_mainnet.json` configuration file is added, please use this file
to initialize a database for MainNet nodes. Ensure your MainNet node running script
contains an updated network ID (`47763`) and a proper MainNet bootnode identifiers
set. For T4 node operators it should be noted that this release does not require a DB
resynchronisation. However, we recommend to update the node binary since it contains
a dBFT-related bug fix that may affect the consensus functionality in a very specific
set of conditions.

New features:
 * Multisignature system contract aimed to serve as an owner of Bridge and Bridge
   Management system contracts instead of a simple EOA account (#279)
 * genesis configuration for Neo X MainNet (#271)

Behavior changes:
 * initial candidate registration fee is lowered down to 1000 GAS in Governance
   system contract (#275, #281)
 * candidate exit fee ratio is increased up to 50% from the deposited value in
   Governance system contract (#275, #280, #281) 

Improvements:
 * disable initialization code for every UUPS implementation (#259)
 * Bridge and Bridge Management system contracts update (#274)

Bugs fixed:
 * missing total votes update on transferring votes from exited candidate in
   Governance system contract (#260)
 * non-strict GovernanceVote system contract check for the number of voters (#267)
 * improper GAS estimation in `eth_estimateGas` Blockchain RPC API caused by changes
   in fee policies of Neo X (#263)
 * missing track of miner's work resume event which leads to dBFT hanging on next
   block awaiting when miner is suspended due to the node sync (#268)


## 0.2.0 "Vitaminization" (10 Jul 2024)

A minor long-term supported version bringing new NeoXBurn hardfork that enables
Policy-based transaction fees burning instead of dynamically evaluated fees of
EIP-1559. This version contains compatible protocol extensions (transactions
reannouncement mechanism), a set of enhanced system contract improvements
(consensus candidates limit restrictions, registration deposit fee, voting transfer
functionality, new Treasury system contract and more) and documentation upgrades.
Additionally, as a result of security audit, this version contains a set of
enhancements and stability/safety fixes for system contracts and dBFT protocol.
This version is still based on v1.13.11 Geth implementation with Shanghai hardfork
supported as the latest one.

This version is aimed to be run on a fresh Neo X T4 network. Please, ensure your node
configuration includes the NeoXBurn hardfork, an upgraded network ID and all genesis
allocations properly set. A new database should be initialized for this version, it's
not compatible with an existing Neo X T3 testnet.

New features:
 * Treasury system contract aimed to fund Bridge operations (#184, #236)
 * reannouncement mechanism for pending mempooled transactions (#194)
 * reserved system contracts support (#236)

Behavior changes:
 * limit the number of candidates via Policy system contract (#216, #238)
 * NeoXBurn hardfork introducing BaseFee burning based on the Policy system contract
   setting (#166, #230, #249)
 * time lock is added for system contracts upgrade (#245)
 * 5% deposit fee is charged by Governance on candidate exiting from the Governance
   candidates list (#247)
 * candidate registration fee is increased from 1000 up to 20000 GAS (#236)

Improvements:
 * system contracts documentation updates (#173, #236)
 * `transferVote` method added to the Governance system contract (#182)
 * updated dBFT library dependency (#185)
 * optimize execution cost of system contracts (#195)
 * better event indexing for system contracts (#196)
 * Solidity compiler version upgraded for system contracts (#208)
 * improved error reporting for system contracts (#209)
 * better unit-test coverage for system contracts (#210, #211, #214, #215, #223)
 * clear votes for method call once enough votes are collected in GovernanceVote
   system contract (#227)
 * system contracts stability improvements (#217, #221, #241)

Bugs fixed:
 * reentrancy problem in GovernanceVote system contract (#195)
 * potential division by zero in Governance system contract (#225)
 * OnPersist system call processing missed in t8ntool execution flow (#229)
 * blacklisted accounts are allowed to be elected as consensus members (#242)
 * dBFT protocol payloads are not verified against consensus members by non-consensus
   nodes (#243)

## 0.1.1 "Backdating" (18 April 2024)

An urgent patch release to fix the bug in T3 genesis configuration file introduced in 0.1.0.
Also contains a fix of the bug preventing stale consensus node from continuous state sync
process.

Please, note that this release contains T3 genesis configuration file changes and thus,
incompatible with 0.1.0 version. Node operators should regenerate genesis block based on
the updated configuration if upgrading from 0.1.0 version.

Bugs fixed:
* missing initial balance for Bridge relayer in T3 genesis configuration (#174)
* consensus node lock possible during the state sync process for stale nodes (#176)

## 0.1.0 "Attraction" (16 April 2024)

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
