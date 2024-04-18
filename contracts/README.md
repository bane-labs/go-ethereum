# NeoX System Contracts

NeoX system contracts are a set of pre-compiled Solidity codes with pre-fixed contract addresses. They represent the governance and economic model of NeoX, which is fully decentralized and transparent.

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

## GovernanceVote

[GovernanceVote](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/GovernanceVote.sol) is a public "library" that is widely used in system contract management especially upgrade.

Any contract inherits `GovernanceVote.sol` can set up a consensus vote on method execution, by calling internal `vote(bytes32 methodKey, bytes32 paramKey)`, which requires **more than half** of the **current consensus** votes for **the same method call and the same calling parameters**.

1. More than half - the threshold value is `1/2` instead of `2/3`;
2. Current consensus - if an address is no longer a consensus member, its votes will not be counted;
3. The same method and parameters - it means the majority votes for the same execution result.

## GovProxyAdmin

[GovProxyAdmin](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/GovProxyAdmin.sol) controls the upgrade of other pre-compiled system contracts, since all of their `onlyOwner`/`onlyAdmin` point to `0x1212000000000000000000000000000000000000`.

This contract inherits `GovernanceVote.sol` so that it requires a `50%` majority votes among current consensus to execute `upgradeAndCall()`, which means **more than half** of the **current consensus** votes for **the same contract implementation**.

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

At the end of every election epoch, the 7 candidates with the highest amount of votes will be selected by Governance and become block validators of the next `60480` blocks. However, this replacement has two prerequisites.

1. The size of candidate list is larger than `7`;
2. The amount of total valid votes is higher than `3,000,000 GAS`.

Otherwise, the block validators of the next `60480` blocks will be the following pre-fixed stand-by members.

|Testnet Stand-by Address|
|--|
|`0xcbbeca26e89011e32ba25610520b20741b809007`|
|`0x4ea2a4697d40247c8be1f2b9ffa03a0e92dcbacc`|
|`0xd10f47396dc6c76ad53546158751582d3e2683ef`|
|`0xa51fe05b0183d01607bf48c1718d1168a1c11171`|
|`0x01b517b301bb143476da35bb4a1399500d925514`|
|`0x7976ad987d572377d39fb4bab86c80e08b6f8327`|
|`0xd711da2d8c71a801fc351163337656f1321343a0`|

### Reward

The reward distribution of NeoX Governance happens real time, both to validators and voters. Once a candidate is selected as a block validator, it automatically begins to receive `GAS` rewards by participanting DBFT consensus.

The governance reward in NeoX is always distributed twice, first among validators and second between validators and voters.

#### Validator Distribution

Regardless of consensus leader and received vote amount, all of the **transaction priority fees** are **equally divided** among validators as block rewards.

$validatorReward=totalNetworkTips/7$

In NeoX DBFT, the block coinbase address is always `0x1212000000000000000000000000000000000003`, which means the rewards are first minted to [GovReward](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/GovReward.sol) and then transfered to [Governance](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/Governance.sol) through `OnPersist()`.

#### Voter Distribution

If the `shareRate` of a validator is higher than `0`, then `validatorReward` will be distributed again between the validator and its voters.

For the validator, $reward=validatorReward*(1-shareRate)/1000$.

For each of its voters, $reward=(validatorReward*shareRate/1000)*(voteAmount/validatorTotalReceivedVoteAmount)$.

The higher weight a voter has of the validator's received votes, the more `GAS` rewards he can share from NeoX Governance.

The rewards for validators will be immediately sent to their addresses, but the reward settlement for voters has some other rules.

1. The rewards after your first vote but before the next epoch starts is unclaimable, which means you cannot benefit without participanting and affecting any election;
2. Claimable rewards require a `claimReward()` calling to `0x1212000000000000000000000000000000000001` to be released;
3. The settlement happens real time, so your rewards will be transfered as well when the vote amount changes through `vote()` or `revokeVote()`.

There are several special cases of reward distribution,

1. When block validators are stand-by validators, they will not share any reward to the network. This happens in `Epoch 0` and when election result is not valid;
2. Voter rewards will not disappear if your candidate exits but remain claimable. However, it is possiable that your candidate exits and returns with a different `shareRate` after 2 epochs. It will influence your future benefits so keep eyes on candidate's activities.

## Policy

[Policy](https://github.com/bane-labs/go-ethereum/blob/bane-main/contracts/solidity/Policy.sol) controls the global settings of NeoX protocol, which are forced on every running nodes.

The current NeoX Policy maintains following parameters. All these policies are both checked by nodes locally and by DBFT globally.

|Name|Parameter|Usage|
|--|--|--|
|Address Blacklist|`isBlackListed`|Prevent blacklisted addresses to send transactions in NeoX network|
|Minimum Transaction Tip Cap|`minGasTipCap`|Force transaction senders to pay a minimum tip to NeoX Governance|
|Network Base Fee|`baseFee`|Force block validators to burn a part of transaction fees (TBD)|

Since all the policy setters adopt the `needVote` modifier, any policy change requires `1/2` vote pass by current NeoX consensus.

## Bridge

Refer to [bridge repo](https://github.com/bane-labs/bridge-evm-contracts).