# Changelog

This document outlines major changes between releases.

## 0.4.2 "Mutualization" (2 Sep 2025)

A couple of ZK-based DKG rounds passed on TestNet, hence it may be safely
enabled on MainNet. This version reschedules NeoXDKG, NeoXAMEV and NeoXEthSig
forks to enable them earlier than planned initially. This version also adds an
availability check for DKG-related files on node bootstrap.

This version is fully compatible with v0.4.1 and does not require node
resynchronization. For TestNet nodes no configuration changes are required on
upgrade (comparing to v0.4.1). For MainNet nodes the DB reinitialization is
required with the updated genesis configuration since `NeoXEthSig`, `NeoXDKG`
and `NeoXAMEV` forks are rescheduled. Follow the instructions below to upgrade
your node from v0.4.1 to v0.4.2:

1. Download new binary and new genesis configuration file from the release page.
2. Gracefully stop the node.
3. Replace the old binary with the new binary.
4. For MainNet nodes only: don't remove DB; reinitialize DB using new binary and
   new genesis configuration file with the following command:
   ```
   ./geth init --datadir ./node-datadir ./config/genesis.json
   ```
5. Start the node.

Behaviour changes:
 * NeoXDKG, NeoXAMEV and NeoXEthSig forks of MainNet are rescheduled to 3623040,
   3689280 and 3689280 blocks correspondingly (#512)
 * reschedule NeoXDKG fork for PrivNet setups (#502)

Improvements:
 * check DKG-related files are available on node startup (#503)
 * remove compatibility code from system contracts (#506)
 * update ZK-PrivNet documentation (#511)

## 0.4.1 "Liberalization" (19 Aug 2025)

This patch-release supports recovery from out-of-date or lost anti-MEV keystore
and introduces an enhanced version of ZK-based DKG process. The results of MPC
ceremony for ZK-DKG setup conducted by NGD, NSPCC, AxLabs and Lazynode are
published to the https://github.com/bane-labs/mpc repository and integrated to
the verifier system contracts. This release schedules `NeoXEthSig`, `NeoXDKG`
and `NeoXAMEV` forks for MainNet with ZK-based DKG version.

This version is fully compatible with v0.4.0 and does not require node
resynchronization. For TestNet nodes no configuration changes are required on
upgrade (comparing to v0.4.0). For MainNet nodes the DB reinitialization is
required with the updated genesis configuration since `NeoXEthSig`, `NeoXDKG`
and `NeoXAMEV` forks are enabled. Follow the instructions below to upgrade your
node from v0.4.0 to v0.4.1:

1. Download new binary and new genesis configuration file from the release page.
2. Gracefully stop the node.
3. Replace the old binary with the new binary.
4. For MainNet nodes only: don't remove DB; reinitialize DB using new binary and
   new genesis configuration file with the following command:
   ```
   ./geth init --datadir ./node-datadir ./config/genesis.json
   ```
5. For consensus nodes only: prepare for participation in ZK-based DKG process
   following the steps below:
      1. Download 3 pairs of R1CS files and proving key files from the
         [Neo X MPC ceremony page](https://github.com/bane-labs/mpc?tab=readme-ov-file#seal-result)
         using [NeoFS CLI](https://github.com/nspcc-dev/neofs-node/releases/tag/v0.48.3):
         ```
         mkdir ./r1cs
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid 8f6m4RUvgNDyo7gdFhEJQEAqkdaTzEj4oYuLVxfRJP4S -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./r1cs/one_message.ccs
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid Fqzn6PvAhmmYWVBRV8dVRWwAL3T8JHgycBfjS7A18z6f -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./r1cs/two_message.ccs
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid 2jNd8acKHBb5s6matnED49MCo36vTMWXbfjocT7Xcub7 -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./r1cs/seven_message.ccs
         mkdir ./pk
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid HZVzrU7348zztWvgBTM3xkpvZ6BNNJMGDrKyeDDTHZLw -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./pk/one_message.pk
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid HKEeCskBjnL5yJGXYP4EfakVaDsw3aAJ64FXavDhpv4E -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./pk/two_message.pk
         ./neofs-cli-linux-amd64 object get --cid 411d8vuzogogMxXJqTQcu61btgQ6rL2VNYUYnH7r4kE3 --oid A1DTHYvdnzrgEJP14yzt2T8AXsuM3YaNDoe3LoMWXepT -r grpc://st3.storage.fs.neo.org:8080 --timeout 1000s --file ./pk/seven_message.pk
         ```
      2. Specify paths to the downloaded files to the node's run command via
         the following flags:
          ```
          --dkg.one-msg-r1cs=./r1cs/one_message.ccs \
          --dkg.two-msg-r1cs=./r1cs/two_message.ccs \
          --dkg.seven-msg-r1cs=./r1cs/seven_message.ccs \
          --dkg.one-msg-pk=./pk/one_message.pk \
          --dkg.two-msg-pk=./pk/two_message.pk \
          --dkg.seven-msg-pk=./pk/seven_message.pk \
          ```
      3. Ensure that `--antimev.password` flag is provided to the node's
         run command and the node has access to its anti-MEV keystore.
      4. Ensure that your node meets the hardware requirements mentioned in the
         [README](https://github.com/bane-labs/go-ethereum?tab=readme-ov-file#consensus-node)
         since ZK-based DKG process significantly increases node's RAM
         consumption. If not, then migrate your node to the suitable machine.
6. Start the node.

New features:
 * the results of MPC ceremony are integrated into verifier contracts (#494)

Behaviour changes:
 * BLS12-381 precompiles are updated to Prague-compatible version (#488, #495)
 * `NeoXDKG` fork is enabled at height `3689280` of MainNet (#497)
 * `NeoXEthSig` fork is enabled at height `3810240` of MainNet (#497)
 * `NeoXAMEV` fork is enabled at height `3810240` of MainNet (#497)

Improvements:
 * support out-of-date or lost anti-MEV keystore recovery (#480, #491)
 * an upgrade to optimized `zk-dkg` v0.3.0 (#492)
 * ZK-related KeyManagement system contract updates (#489)

Bugs fixed:
 * panic on ZK-based DKG message recovery (#485)
 * disabled shares phase check during DKG epoch transition (#480)
 * improper ZK version is used during reshare recovery (#480)
 * zero random scalar is possible in anti-MEV related operations (#490)

## 0.4.0 "Karstification" (20 Jun 2025)

This version introduces a preview of ZK-based DKG signature verification added to
KeyManagement system contract and the support of ZK-based DKG signature generation
at the dBFT side (this feature will be auto-enabled by dBFT once KeyManagement
contract is upgraded by the network maintainers). Note that this feature is not yet
finalized and is a subject of polishing for further releases. Also, this version adds
a support for `NeoXEthSig` fork that switches to a canonical form of TPKE block
signatures. In addition to that, this version contains a set of optimisations improving
the efficiency of block producing process and a set of dBFT-related bug fixes.

This version is fully compatible with v0.3.2 and does not require DB resync. For TestNet
nodes upgrade, the DB reinitialization with updated genesis configuration is required
(`NeoXEthSig` fork is scheduled at block 3750000 of TestNet). To upgrade MainNet nodes
from v0.2.2, no DB reinitialization is required (since we don't yet enable `NeoXDKG`,
`NeoXAMEV` and `NeoXEthSig` forks on MainNet), but anti-MEV keystore should be generated
and provided to the node on startup following the notes from v0.3.0 release.

New features:
 * `NeoXEthSig` hardfork fixing the format of TPKE block signature (#463)
 * ZK-based DKG signature verification support in KeyManagement contract (#442, #470)
 * ZK-based DKG signature generation support in dBFT (#444)

Behaviour changes:
 * `txpool.signaturecache` CLI flag is renamed to `txpool.amevcache` (#457)

Improvements:
 * configurable dBFT status statistics period (#449)
 * documentation improvements (#457)
 * refactoring of encrypted transactions pool (#468)
 * optimize conversion of dBFT CN index to DKG CN index (#458)
 * use local mempool for dBFT proposal verification (#466, #476)

Bugs fixed:
 * panic in encrypted transactions pool on attempt to remove expired transaction (#447)
 * empty pending consensus list on new epoch start in Governance contract (#460)
 * DKG routine is not synchronized with miner interruption (#475)
 * panic on dBFT payload processing during miner node shutdown (#467)
 * non-canonical format of TPKE block signature (#463)
 * panic in dBFT on PreBlock verification (#473)
 * consensus doesn't work on single-node privnet (#315)
 * inability to retrieve ZK version from old KeyManagement implementation (#478)

## 0.3.2 "Ionization" (20 Mar 2025)

This is an urgent patch-release fixing a panic happened on primary CN during decrypted
transaction verification. This version is compatible with v0.3.1 and does not require
DB resync or configuration update on upgrade.

Behaviour changes:
* `NeoXAMEV` fork is enabled at height `2088000` of Testnet (#437)

Bugs fixed:
* decrypted transaction can't be verified at primary node due to panic (#439)

## 0.3.1 "Zonation" (11 Mar 2025)

This patch-release introduces `NeoXDKG` fork on Testnet and contains a couple of minor
improvements compatible with v0.3.0 version.

Please, follow the notes to upgrade your node from v0.3.0 to v0.3.1:
1. Download new binary and new genesis configuration file from the release page.
2. Gracefully stop the node.
3. Replace the old binary with the new binary.
4. Don't remove DB. Reinitialize DB using new binary and new configuration file for the
   corresponding network with the following command:
   ```
   ./geth init --datadir ./node-datadir ./config/genesis.json
   ```
5. If you're running a consensus node, ensure that `--antimev.password` flag is provided
   to the node's runner script and the node has access to its anti-MEV keystore. Also, 
   ensure that you've registered your node's anti-MEV public key in the KeyManagement
   contract.
6. Start the node.

Behaviour changes:
 * `NeoXDKG` fork is enabled at height `1990080` of Testnet (#434)

Improvements:
 * anti-MEV related documentation upgrade (#430)
 * anti-MEV public key is required to register new candidate via Governance contract (#433)

Bugs fixed:
 * encrypted transaction fee wasn't checked at the consensus/state DB level (#431)
 * Governance contract wasn't allow to register a candidate (#433)

## 0.3.0 "Elation" (27 Feb 2025)

This version introduces support for two major features: anti-MEV encrypted transactions
processing and threshold BLS block signatures. Two new forks are implemented to provide these
features: `NeoXDKG` enables new system KeyManagement contract that allows consensus nodes
to participate in the DKG process, and `NeoXAMEV` that enables encrypted transactions processing
at the consensus level and BLS threshold block signatures which solves the frequent chain reorg
problem.

Please, follow carefully the notes to upgrade your node from v0.2.2 to v0.3.0:
1. Download new binary and new genesis configuration file
   (`go-ethereum/config/genesis_mainnet.json` or `go-ethereum/config/genesis_testnet.json`)
   from the release page.
2. Gracefully stop the node.
3. Replace the old binary with the new binary.
4. Don't remove DB. Reinitialize DB using new binary and new configuration file for the
   corresponding network with the following command:
   ```
   ./geth init --datadir ./node-datadir ./config/genesis.json
   ```
5. If you're running a consensus node, ensure this step is executed in a safe environment.
   Generate anti-MEV keystore for your consensus node with new binary using the following
   command:
   ```
   ./geth --datadir ./node-datadir antimev init <address>
   ```
     - `<address>` is the address of your consensus node that is used to participate in the
       consensus process, it can be found in the consensus node wallet.
   You will be prompted for a password. Ensure that you remember the password.
6. Adjust your node's runner script:
   * For consensus nodes only: add a password for anti-MEV keystore using
     `--antimev.password ./password.txt` flag.
   * Optional: if you'd like to change the dBFT log level from default (`info`), specify it
     via `--dbft.loglevel debug` flag.
7. Start the node.

New features:
 * anti-MEV keystore support for the node and CLI and related TPKE cryptography
   implementation (#301, #326, #332, #371, #392, #394)
 * new KeyManagement system contract (#312, #332, #398)
 * anti-MEV enabled dBFT consensus support (#287)
 * NeoXDKG fork that enables KeyManagement system contract operations, BLS
   precompiles and MCOPY opcode (#328, #332, #402, #409)
 * NeoXAMEV fork that enables anti-MEV logic, encrypted transactions processing for
   consensus engine and threshold block signatures (#321, #347, #358, #384, #383,
   #385, #391, #393, #389, #392, #416, #418, #421)

Behaviour changes:
 * NeoXBurn fork is removed (#328)
 * OnPersist system script execution now includes KeyManagement system contract call
   (#330)
 * reduce block acceptance interval to 5 seconds (#369)
 * Governance system contract voting lock is removed, logic distribution scheme is
   adjusted to be seamless (#349)

Improvements:
 * enhanced transactions verification logic in consensus engine (#339)
 * Governance contract updates required for DKG setup (#337)
 * watch-only consensus nodes are added to privnet (#359)
 * maintain processed block state in consensus engine of every consensus node (#360)
 * possibility of fallback from threshold block signature to multisignature (#366,
   #390)
 * stabilize block generation time (#396)
 * make GovernanceReward contract accept Envelope transfers (#400)
 * ability to customize dBFT log messages level (#419)
 * anti-MEV transactions policies (#413, #422)

Bugs fixed:
 * extensible payload senders are not properly verified (#319)
 * watch-only consensus node is able so send consensus messages (#351)
 * unexpected timeout for system contract calls (#379)
 * new blocks are not tracked during sealing proposal awaiting (#404)
 * RecoveryMessage encoding format incompatibility (#425)

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
