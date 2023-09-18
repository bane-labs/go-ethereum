
// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.9;
import "./GovernanceVote.sol";

contract Policy is GovernanceVote{
   
    uint public  minGasPrice ;
    mapping(address=>bool) public isBlackListed;    
    bool private initialized;
   

    function initialize() public {
        require(!initialized, "Contract instance has already been initialized");
        minGasPrice = 10000000;       
        initialized = true;
    }

    // set minimum gasprice
    function setMinGasPrice (uint _gasPrice) external needVote(keccak256("setMinGasPrice"), keccak256(abi.encode(_gasPrice)))  {
        require(_gasPrice > 0, "Policy: setMinGasPrice invalid parameter");
        minGasPrice = _gasPrice;    
    }   

    //  cancel blacklist
    function addBlackList (address _addr) external needVote(keccak256("addBlackList"), keccak256(abi.encode(_addr))) {
        require(!isBlackListed[_addr],"Policy: Blacklist already exists");
        isBlackListed[_addr] = true;        
    }
    //  cancel blacklist
    function removeBlackList (address _addr) external needVote(keccak256("removeBlackList"), keccak256(abi.encode(_addr))) {
        require(isBlackListed[_addr],"Policy: Blacklist does not exist");
        isBlackListed[_addr] = false;        
           
    }

}
