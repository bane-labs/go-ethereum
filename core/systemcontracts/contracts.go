package systemcontracts

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// governanceABI is an ABI of system governing contract.
const governanceABI = `[{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"CandidateWithdraw","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"}],"name":"Exit","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address[]","name":"validators","type":"address[]"}],"name":"Persist","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"}],"name":"Register","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"voter","type":"address"},{"indexed":false,"internalType":"address","name":"from","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"Revoke","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"voter","type":"address"},{"indexed":false,"internalType":"address","name":"to","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"Vote","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"voter","type":"address"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"}],"name":"VoterClaim","type":"event"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"candidateBalanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"candidateGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"claimReward","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"consensusSize","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"currentConsensus","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"currentEpochStartHeight","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"epochDuration","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"},{"internalType":"uint256","name":"","type":"uint256"}],"name":"epochStartGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"exitCandidate","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"exitHeightOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getCandidates","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getCurrentConsensus","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"govReward","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"minVoteAmount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"onPersist","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"receivedVotes","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"shareRate","type":"uint256"}],"name":"registerCandidate","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"registerFee","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"revokeVote","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"scaleFactor","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"shareRateOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidateTo","type":"address"}],"name":"vote","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"voteHeight","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"votedAmount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"votedTo","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"voterGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"withdrawRegisterFee","outputs":[],"stateMutability":"nonpayable","type":"function"},{"stateMutability":"payable","type":"receive"}]`

const policyABI = `[{"inputs":[{"internalType":"address","name":"target","type":"address"}],"name":"AddressEmptyCode","type":"error"},{"inputs":[{"internalType":"address","name":"implementation","type":"address"}],"name":"ERC1967InvalidImplementation","type":"error"},{"inputs":[],"name":"ERC1967NonPayable","type":"error"},{"inputs":[],"name":"FailedInnerCall","type":"error"},{"inputs":[],"name":"InvalidInitialization","type":"error"},{"inputs":[],"name":"NotInitializing","type":"error"},{"inputs":[],"name":"UUPSUnauthorizedCallContext","type":"error"},{"inputs":[{"internalType":"bytes32","name":"slot","type":"bytes32"}],"name":"UUPSUnsupportedProxiableUUID","type":"error"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"addr","type":"address"}],"name":"AddBlackList","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint64","name":"version","type":"uint64"}],"name":"Initialized","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"addr","type":"address"}],"name":"RemoveBlackList","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"baseFee","type":"uint256"}],"name":"SetBaseFee","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"gasTipCap","type":"uint256"}],"name":"SetMinGasTipCap","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"implementation","type":"address"}],"name":"Upgraded","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"voter","type":"address"},{"indexed":false,"internalType":"bytes32","name":"methodKey","type":"bytes32"},{"indexed":false,"internalType":"bytes32","name":"paramKey","type":"bytes32"}],"name":"Vote","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"bytes32","name":"methodKey","type":"bytes32"},{"indexed":false,"internalType":"bytes32","name":"paramKey","type":"bytes32"}],"name":"VotePass","type":"event"},{"inputs":[],"name":"GOV_ADMIN","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"SELF","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"UPGRADE_INTERFACE_VERSION","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_addr","type":"address"}],"name":"addBlackList","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"baseFee","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"govReward","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"isBlackListed","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"addr","type":"address"}],"name":"isMiner","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"minGasTipCap","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"proxiableUUID","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_addr","type":"address"}],"name":"removeBlackList","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_baseFee","type":"uint256"}],"name":"setBaseFee","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_gasTipCap","type":"uint256"}],"name":"setMinGasTipCap","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"newImplementation","type":"address"},{"internalType":"bytes","name":"data","type":"bytes"}],"name":"upgradeToAndCall","outputs":[],"stateMutability":"payable","type":"function"}]`

// some storage slot indexes of policy contract.
const blackListSlotIndex = 1
const minGasTipCapSlotIndex = 2
const baseFeeSlotIndex = 3

// A set of genesis contracts.
var (
	// GovernanceProxyHash is a governing contract hash that manages validators
	// voting and rewards.
	GovernanceProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000001")
	// GovernanceABI is a compiled ABI of Governance contract.
	GovernanceABI abi.ABI
	// PolicyProxyHash is a management contract hash for system policies.
	PolicyProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000002")
	// PolicyABI is a compiled ABI of Policy contract.
	PolicyABI abi.ABI
)

func init() {
	var err error
	GovernanceABI, err = abi.JSON(strings.NewReader(governanceABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode Governance contract ABI: %w", err))
	}
	PolicyABI, err = abi.JSON(strings.NewReader(policyABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode Policy contract ABI: %w", err))
	}
}

// GetMinGasTipCapStateHash computes and returns the storage key of minGasTipCap
// in policy contract, for reading corresponding values from statedb.
func GetMinGasTipCapStateHash() common.Hash {
	return common.BytesToHash([]byte{minGasTipCapSlotIndex})
}

// GetBaseFeeStateHash computes and returns the storage key of baseFee
// in policy contract, for reading corresponding values from statedb.
func GetBaseFeeStateHash() common.Hash {
	return common.BytesToHash([]byte{baseFeeSlotIndex})
}

// GetBlackListStateHash computes and returns the storage key of blockList
// in policy contract with an address, for reading corresponding values from statedb.
func GetBlackListStateHash(addr common.Address) common.Hash {
	return crypto.Keccak256Hash(common.LeftPadBytes(addr.Bytes(), 32), common.LeftPadBytes([]byte{blackListSlotIndex}, 32))
}
