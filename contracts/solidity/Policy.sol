
// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.9;
import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";
import "./GovernanceVote.sol";

contract Policy is GovernanceVote{
    using EnumerableSet for EnumerableSet.AddressSet;   
    uint public  minGasPrice ;
    mapping(address=>bool) public isBlackListed;
    EnumerableSet.AddressSet  blackList;
    bool private initialized;
   

    function initialize()public{
        require(!initialized, "Contract instance has already been initialized");
        minGasPrice = 10000000;       
        initialized = true;
	}

    // set minimum gasprice
    function setMinGasPrice (uint _gasPrice) external needVote(keccak256("setMinGasPrice"), keccak256(abi.encode(_gasPrice)))  {
        require(_gasPrice > 0, "Policy: setMinGasPrice invalid parameter");
        minGasPrice = _gasPrice;    
    }   

    //  add/cancel blacklist
    function setBlackList (address _addr,bool _isBlackListed) external needVote(keccak256("setBlackList"), keccak256(abi.encode(_addr,_isBlackListed))) {
        isBlackListed[_addr] = _isBlackListed;  
        if (_isBlackListed){
           require( blackList.add(_addr), "Policy: blacklisted add error");
        }else{
           require( blackList.remove(_addr), "Policy: blacklisted remove error");
        }
           
    }

    // get blacklist
    function getBlackList() public view returns (address[] memory){  
        return blackList.values();
    }
 
}
