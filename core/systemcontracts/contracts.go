package systemcontracts

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// System contracts ABI.
const (
	// governanceABI is an ABI of system governing contract.
	governanceABI = `[{"inputs":[{"internalType":"address","name":"target","type":"address"}],"name":"AddressEmptyCode","type":"error"},{"inputs":[],"name":"CandidateExists","type":"error"},{"inputs":[],"name":"CandidateNotExists","type":"error"},{"inputs":[],"name":"CandidateWithdrawNotAllowed","type":"error"},{"inputs":[{"internalType":"address","name":"implementation","type":"address"}],"name":"ERC1967InvalidImplementation","type":"error"},{"inputs":[],"name":"ERC1967NonPayable","type":"error"},{"inputs":[],"name":"FailedInnerCall","type":"error"},{"inputs":[],"name":"InsufficientValue","type":"error"},{"inputs":[],"name":"InvalidInitialization","type":"error"},{"inputs":[],"name":"InvalidShareRate","type":"error"},{"inputs":[],"name":"LeftNotClaimed","type":"error"},{"inputs":[],"name":"MultipleVoteNotAllowed","type":"error"},{"inputs":[],"name":"NoVote","type":"error"},{"inputs":[],"name":"NotAdmin","type":"error"},{"inputs":[],"name":"NotInitializing","type":"error"},{"inputs":[],"name":"OnlyEOA","type":"error"},{"inputs":[],"name":"ReentrancyGuardReentrantCall","type":"error"},{"inputs":[],"name":"RegisterDisabled","type":"error"},{"inputs":[],"name":"SameCandidate","type":"error"},{"inputs":[],"name":"SideCallNotAllowed","type":"error"},{"inputs":[],"name":"TransferFailed","type":"error"},{"inputs":[],"name":"UUPSUnauthorizedCallContext","type":"error"},{"inputs":[{"internalType":"bytes32","name":"slot","type":"bytes32"}],"name":"UUPSUnsupportedProxiableUUID","type":"error"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"}],"name":"Activate","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"CandidateWithdraw","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"candidate","type":"address"}],"name":"Deactivate","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint64","name":"version","type":"uint64"}],"name":"Initialized","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address[]","name":"validators","type":"address[]"}],"name":"Persist","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"voter","type":"address"},{"indexed":true,"internalType":"address","name":"from","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"Revoke","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"implementation","type":"address"}],"name":"Upgraded","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"voter","type":"address"},{"indexed":true,"internalType":"address","name":"to","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"Vote","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"voter","type":"address"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"}],"name":"VoterClaim","type":"event"},{"inputs":[],"name":"EXIT_FEE_RATE","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"GOV_ADMIN","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"GOV_REWARD","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"POLICY","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"SCALE_FACTOR","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"SELF","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"SYS_CALL","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"UPGRADE_INTERFACE_VERSION","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidate","type":"address"}],"name":"activateCandidate","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"blacklistedCandidates","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"candidateBalanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"candidateGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"claimReward","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"consensusSize","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"currentConsensus","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"currentEpochStartHeight","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidate","type":"address"}],"name":"deactivateCandidate","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"epochDuration","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"},{"internalType":"uint256","name":"","type":"uint256"}],"name":"epochStartGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"exitCandidate","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"exitHeightOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getCandidates","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getCurrentConsensus","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"minVoteAmount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"onPersist","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"onPersistV2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"proxiableUUID","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"receivedVotes","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"shareRate","type":"uint256"}],"name":"registerCandidate","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"registerFee","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"revokeVote","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"shareRateOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"standByValidators","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"totalVotes","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"candidateTo","type":"address"}],"name":"transferVote","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"voter","type":"address"}],"name":"unclaimedRewardOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"newImplementation","type":"address"},{"internalType":"bytes","name":"data","type":"bytes"}],"name":"upgradeToAndCall","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[{"internalType":"address","name":"candidateTo","type":"address"}],"name":"vote","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"voteHeight","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"voteTargetAmount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"votedAmount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"votedTo","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"voterGasPerVote","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"withdrawRegisterFee","outputs":[],"stateMutability":"nonpayable","type":"function"},{"stateMutability":"payable","type":"receive"}]`
	// keyManagementABI is a partial ABI of system anti-MEV KeyManagement contract,
	// it contains a minimum required list of methods needed for system interactions.
	keyManagementABI = `[{"inputs":[],"name":"onPersistV2","outputs":[],"stateMutability":"nonpayable","type":"function"}]`
)

// some storage slot indexes of policy contract.
const blackListSlotIndex = 1
const minGasTipCapSlotIndex = 2
const baseFeeSlotIndex = 3

// A set of genesis contract hashes.
var (
	// GovernanceProxyAdminHash is a hash of the GovernanceProxyAdmin contract that
	// that manages GovernanceProxy contract upgrades.
	GovernanceProxyAdminHash = common.HexToAddress("0x1212000000000000000000000000000000000000")
	// GovernanceProxyHash is a hash of GovernanceProxy contract that manages validators
	// voting and rewards.
	GovernanceProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000001")
	// PolicyProxyHash is a hash of PolicyProxy contract that manages system policy settings.
	PolicyProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000002")
	// GovernanceRewardProxyHash is a hash of GovernanceRewardProxy contract that manages
	// CN and voters reward distribution logic.
	GovernanceRewardProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000003")
	// BridgeProxyHash is a hash of the BridgeProxy contract that manages Bridge funds.
	BridgeProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000004")
	// BridgeManagementProxyHash is a hash of the BridgeManagementProxy contract that
	// manages Bridge operations.
	BridgeManagementProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000005")
	// TreasuryHash is a hash of the Treasury contract that contains all Bridge
	// funds at the start of the network. Note that this contract is not upgradeable.
	TreasuryHash = common.HexToAddress("0x1212000000000000000000000000000000000006")
	// CommitteeMultiSigProxyHash is a hash of the CommitteeMultiSigProxy contract that
	// manages external invocations which needs a Committee agreement.
	CommitteeMultiSigProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000007")
	// KeyManagementProxyHash is a hash of KeyManagementProxy contract that manages
	// distributed keys generation and lifecycle.
	KeyManagementProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000008")
	// Reserved1ProxyHash is a hash of the reserved system contract #1.
	Reserved1ProxyHash = common.HexToAddress("0x1212000000000000000000000000000000000009")
	// Reserved2ProxyHash is a hash of the reserved system contract #2.
	Reserved2ProxyHash = common.HexToAddress("0x121200000000000000000000000000000000000a")
)

// A set of genesis contract ABIs.
var (
	// GovernanceABI is a compiled ABI of Governance contract.
	GovernanceABI abi.ABI
	// KeyManagementABI is a compiled partial ABI of KeyManagement contract.
	KeyManagementABI abi.ABI
)

func init() {
	var err error
	GovernanceABI, err = abi.JSON(strings.NewReader(governanceABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode Governance contract ABI: %w", err))
	}
	KeyManagementABI, err = abi.JSON(strings.NewReader(keyManagementABI))
	if err != nil {
		panic(fmt.Errorf("failed to decode KeyManagement contract ABI: %w", err))
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
