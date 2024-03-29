// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.20;

import "./GovernanceVote.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract Policy is GovernanceVote, UUPSUpgradeable {
    address public constant GOV_ADMIN =
        0x1212000000000000000000000000000000000000;

    uint256 public minGasPrice;
    mapping(address => bool) public isBlackListed;
    event SetMinGasPrice(uint256 gasPrice);
    event AddBlackList(address addr);
    event RemoveBlackList(address addr);

    modifier onlyAdmin() {
        require(msg.sender == GOV_ADMIN, "Not admin");
        _;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal virtual override onlyAdmin {}

    // set minimum gasprice
    function setMinGasPrice(
        uint256 _gasPrice
    )
        external
        needVote(keccak256("setMinGasPrice"), keccak256(abi.encode(_gasPrice)))
    {
        require(_gasPrice > 0, "Policy: setMinGasPrice invalid parameter");
        minGasPrice = _gasPrice;
        emit SetMinGasPrice(_gasPrice);
    }

    //  cancel blacklist
    function addBlackList(
        address _addr
    )
        external
        needVote(keccak256("addBlackList"), keccak256(abi.encode(_addr)))
    {
        require(!isBlackListed[_addr], "Policy: Blacklist already exists");
        isBlackListed[_addr] = true;
        emit AddBlackList(_addr);
    }

    //  cancel blacklist
    function removeBlackList(
        address _addr
    )
        external
        needVote(keccak256("removeBlackList"), keccak256(abi.encode(_addr)))
    {
        require(isBlackListed[_addr], "Policy: Blacklist does not exist");
        delete isBlackListed[_addr];
        emit RemoveBlackList(_addr);
    }
}
