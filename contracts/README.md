# NeoX System Contracts

NeoX's system contracts are a set of pre-compiled Solidity codes with pre-fixed contract addresses. They represent the governance and economic model of NeoX, which is fully decentralized and transparent.

These contracts are not deployed by transactions but alloced in the [genesis file](https://github.com/bane-labs/go-ethereum/blob/bane-main/config). The address setting of existing pre-compiled contracts is listed as below.

|Address|Contract|
|--|--|
|`0x1212000000000000000000000000000000000000`|GovProxyAdmin|
|`0x1212000000000000000000000000000000000001`|Governance Proxy|
|`0x1212100000000000000000000000000000000001`|Governance Implemention|
|`0x1212000000000000000000000000000000000002`|Policy Proxy|
|`0x1212100000000000000000000000000000000002`|Policy Implemention|
|`0x1212000000000000000000000000000000000003`|GovernanceReward Proxy|
|`0x1212100000000000000000000000000000000003`|GovernanceReward Implemention|
|`0x1212000000000000000000000000000000000004`|Bridge Proxy|
|`0x1212100000000000000000000000000000000004`|Bridge Implemention|
|`0x1212000000000000000000000000000000000005`|BridgeManagement Proxy|
|`0x1212100000000000000000000000000000000005`|BridgeManagement Implemention|

## GovProxyAdmin

[GovProxyAdmin](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/GovProxyAdmin.sol) controls the upgrade of other pre-compiled system contracts, since all of their `onlyOwner`/`onlyAdmin` point to `0x1212000000000000000000000000000000000000`.

This contract inherits `GovernanceVote.sol` so that it requires a `50%` majority votes among current consensus to execute `upgradeAndCall()`, which means **more than half** of the **current consensus** votes for **the same method call and the same calling parameters**.

1. More than half - the threshold value is `1/2` instead of `2/3`;
2. Current consensus - if an address is no longer a consensus member, its votes will not be counted;
3. The same method and parameters - it means the majority votes for the same execution result;

All of the upgradable NeoX system contracts use [ERC1967Proxy](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/release-v5.0/contracts/proxy/ERC1967/ERC1967Proxy.sol) and [UUPSUpgradeable](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/release-v5.0/contracts/proxy/utils/UUPSUpgradeable.sol).

## Governance

[Governance](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/Governance.sol) is responsible for the election of block validators and related reward distribution.

An election is, **GAS holders** vote to **registered candidates** and the Governance contracts selects **top 7 candidates** as block validators for **the next epoch**.

### Candidate

An address can only become a candidate to receive votes after it registers in Governance and stake a minimum register fee. A successful register requires below.

1. Registrant invokes `registerCandidate()` of `0x1212000000000000000000000000000000000001` as message sender;
2. Registrant is an EOA account and not yet a candidate;
3. Put at least `1000 GAS` deposit `value` along with the transaction as register fee;
4. Provide a `shareRate` ranges from `0` to `1000` in parameters which can not be changed until exit;
5. (optional) Withdraw past deposits if has registered and exited before.

After this, a new candidate will appear in the candidate list and can be voted immediately and be elected as one of the block validators of the next epoch.

A candidate can exit without any permission, but it requires 2 epoch times before register fee withdraw. During this period, candidate cannot receive any votes or become a block validator, but voters can revoke their votes and choose other candidates to share rewards.

Note that the current epoch time on testnet is `60480` blocks.

### Election

All GAS holders can vote and benefit from NeoX Governance, including EOA accounts and smart contracts. A successful vote requires below.

1. Voter invokes `vote()` of `0x1212000000000000000000000000000000000001` as message sender;
2. Put at least `1 GAS` vote `value` along with the transaction;
3. The provided `candidateTo` address is listed in the current candidates;
4. (optional) Revoke votes to other candidates if has voted before.

NeoX Governance doesn't allow multi-target votes and doesn't distribute rewards to new voters until a new epoch begins. So be careful to revoke or change your vote target.

...

### Reward

The reward distribution of NeoX Governance happens real time, both to validators and voters.

...

### Policy

...