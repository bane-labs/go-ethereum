# Private Ethereum network (privnet)

Mainly, this document is based on
https://geth.ethereum.org/docs/fundamentals/private-network, with some 
alterations added. It helps set up a testing private Ethereum network (privnet) 
consisting of two nodes. Both nodes will run on the local machine, 
using the same genesis block and network ID.
The data directories for each node will be named `node1` and `node2`. 
Privnet uses the developer tool `bootnode`, and the `bootnode` directory stores
some data for it. Each node uses the bootnode as an entry point. 
`Node1` functions as a signer with Clique as the consensus algorithm, 
while `node2` serves as a simple RPC node.

Here are the instructions for running a private Ethereum network (privnet)
based on the specifications mentioned above:

1. Build `geth`:
```
$ cd go-ethereum
$ make geth
```

2. Ensure that ports 30305, 30306, and 30307 are not in use on your 
localhost (127.0.0.1). Also, ensure that RPC ports 8552 and 8553 are 
not being used.

3. Start the private network by running `make privnet_start`.
Now you can look into the files `privnet/node1/geth_node.log` and
`privnet/node2/geth_node.log` to see the logs. Or you can use the next command:
```
tail -f ./privnet/node1/geth_node.log
```

4. To stop the private network, use the command `make privnet_stop`.

5. If, for any reason, you wish to stop and remove privnet along with all 
the data in the `privnet` directory, you can use the command: 
`make privnet_clean`.

Network/chain identifier, node addresses, and passwords are stored in the file
`privnet/config.json`.

## Commands in JavaScript console

While privnet is started, it is now possible to attach a Javascript console 
to either node to query the network properties:
```
./build/bin/geth attach privnet/node1/geth.ipc
```
Once the Javascript console is running, check that the node is connected 
to one other peer. It should be equal 1.
```
net.peerCount
```

You can check the account balance for each account (find the address in the 
console where you’ve started privnet):
```
eth.getBalance('745c8f1af649651f46dcaec2c6eb94068843ae96')
```

You can send a transaction between these accounts:
```
eth.sendTransaction({
from: '625eafa3473492007c0dd331e23b1035f6a7fb64',
to: '745c8f1af649651f46dcaec2c6eb94068843ae96',
value: 250,
gas_price: 10,
gas: 30000
});
```

# Reinitialize privnet

If you need to regenerate nodes and reinitialize the entire private network, 
please follow the steps below:

1. Initialize the private network by running `make privnet_init`. It cleans 
data corresponding to the current nodes and generates new nodes.

2. Commit changes.
