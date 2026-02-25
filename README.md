## Neo X node based on Go Ethereum

Golang implementation of the Neo X node based on the [Ethereum execution layer](https://github.com/ethereum/go-ethereum).

[![GoDoc](https://godoc.org/github.com/bane-labs/go-ethereum?status.svg)](https://godoc.org/github.com/bane-labs/go-ethereum)
![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/bane-labs/go-ethereum?sort=semver)
[![Discord](https://img.shields.io/badge/discord-join%20chat-blue.svg)](https://discord.gg/n2QmWW9b)

Automated builds are available for stable releases only. Binary archives are
published at https://github.com/bane-labs/go-ethereum/releases.

## Building the source

For prerequisites and detailed build instructions please read the [Installation Instructions](https://xdocs.ngd.network/development/running-a-neo-x-node).

Building `geth` requires both a Go (version 1.23 or later) and a C compiler. You can install
them using your favourite package manager. Once the dependencies are installed, run

```shell
make geth
```

or, to build the full suite of utilities:

```shell
make all
```

## Executables

The go-ethereum project comes with several wrappers/executables found in the `cmd`
directory.

|  Command   | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| :--------: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **`geth`** | Our main Ethereum CLI client. It is the entry point into the Ethereum network (main-, test- or private net), capable of running as a full node (default), archive node (retaining all historical state) or a light node (retrieving data live). It can be used by other processes as a gateway into the Ethereum network via JSON RPC endpoints exposed on top of HTTP, WebSocket and/or IPC transports. `geth --help` and the [CLI page](https://geth.ethereum.org/docs/fundamentals/command-line-options) for command line options. |
|   `clef`   | Stand-alone signing tool, which can be used as a backend signer for `geth`.                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
|  `devp2p`  | Utilities to interact with nodes on the networking layer, without running a full blockchain.                                                                                                                                                                                                                                                                                                                                                                                                                                       |
|  `abigen`  | Source code generator to convert Ethereum contract definitions into easy-to-use, compile-time type-safe Go packages. It operates on plain [Ethereum contract ABIs](https://docs.soliditylang.org/en/develop/abi-spec.html) with expanded functionality if the contract bytecode is also available. However, it also accepts Solidity source files, making development much more streamlined. Please see our [Native DApps](https://geth.ethereum.org/docs/developers/dapp-developer/native-bindings) page for details.                                  |
| `bootnode` | Stripped down version of our Ethereum client implementation that only takes part in the network node discovery protocol, but does not run any of the higher level application protocols. It can be used as a lightweight bootstrap node to aid in finding peers in private networks.                                                                                                                                                                                                                                               |
|   `evm`    | Developer utility version of the EVM (Ethereum Virtual Machine) that is capable of running bytecode snippets within a configurable environment and execution mode. Its purpose is to allow isolated, fine-grained debugging of EVM opcodes (e.g. `evm --code 60ff60ff --debug run`).                                                                                                                                                                                                                                               |
| `rlpdump`  | Developer utility tool to convert binary RLP ([Recursive Length Prefix](https://ethereum.org/en/developers/docs/data-structures-and-encoding/rlp)) dumps (data encoding used by the Ethereum protocol both network as well as consensus wise) to user-friendlier hierarchical representation (e.g. `rlpdump --hex CE0183FFFFFFC4C304050583616263`).                                                                                                                                                                                |

## Running `geth`

Going through all the possible command line flags is out of scope here (please consult our
[CLI Wiki page](https://geth.ethereum.org/docs/fundamentals/command-line-options)),
but we've enumerated a few common parameter combos to get you up to speed quickly
on how you can run your own `geth` instance.

### Hardware Requirements

#### Seed Node

Minimum:

* CPU with 2+ cores
* 4GB RAM
* 200GB free storage space to sync the Mainnet
* 8 MBit/sec download Internet service

Recommended:

* Fast CPU with 4+ cores
* 16GB+ RAM
* High-performance SSD with at least 1TB of free space
* 25+ MBit/sec download Internet service

#### Consensus Node

Minimum:

* Fast CPU with 2+ cores
* 16GB RAM
* 200GB free storage space to sync the Mainnet
* 8 MBit/sec download Internet service

Recommended:

* Fast CPU with 4+ cores
* 32GB+ RAM
* High-performance SSD with at least 1TB of free space
* 25+ MBit/sec download Internet service

### Full node on the main Neo X network

To run a full node on the Neo X Mainnet network follow the steps below.

1. Download the binaries from https://github.com/bane-labs/go-ethereum/releases or
   build the node with the following command:
   ```shell
   make geth
   ```
2. Download the corresponding Neo X Mainnet configuration from the
   [GitHub repository](https://github.com/bane-labs/go-ethereum/blob/bane-main/config/genesis_mainnet.json). 
3. (Only consensus node) Download 3 pairs of R1CS files and proving key files from [Neo X MPC ceremony](https://github.com/bane-labs/mpc) for participanting ZK-DKG.
4. Initialize node database with the downloaded Neo X Mainnet genesis configuration:
   ```shell
   ./geth init --state.scheme hash --datadir ./node ./genesis_mainnet.json
   ```
5. Create an account for the node operation or use an existing one. The following
   command may be used to create a new account (you'll be prompted for a password):
   ```shell
   ./geth --datadir ./node account new
   ```
6. (Only consensus node) Create an antimev keystore for participanting AMEV-dBFT or use an existing one.
   The following command may be used to create a new keystore for your miner account
   (you'll be prompted for a password):
   ```shell
   ./geth --datadir ./node antimev init <address>
   ```
7. Run the node either as a consensus member or as a seed node.
   1. **Running a seed node.** Seed node is a network member that does not take part
      in a consensus process. This node may be used to interact with the Neo X
      network: create accounts; transfer funds; deploy and interact with contracts;
      query node APIs.
      
      To start the seed node use the following shell script as a template:
      ```shell
      #!/bin/bash
      node="./node"

      port=30303
      udpport=30303
      httpport=8551
      rpcport=8561
      wsport=8571

      nohup ./geth \
      --networkid 47763 \
      --nat extip:127.0.0.1 \
      --port $port \
      --discovery.port $udpport \
      --authrpc.port $rpcport \
      --identity $node \
      --maxpeers 50 \
      --syncmode full \
      --gcmode archive \
      --datadir $node \
      --bootnodes "enode://92eec46dd8b67ea8d8999defe0bf2b43d4c4802ed42a430843fec97dafbdc9128849261bdf1a940d431fc61f06a1317f5fc7c0386e18a9bbf951d0ccd8bf4f98@34.42.6.58:30303,enode://f289fb5c83ed39cf7d7aff2727afe70bf7951222c4a9aaef7bcbceef9fd0b53e4b6c9c0e08a50774dfd50d93e83b977932e4780934d379a6a0ac10cc44c6cfdb@34.87.188.162:30303" \
      --http.api eth,net,txpool,web3,dbft \
      --http --http.addr 0.0.0.0 --http.port $httpport --http.vhosts "*" --http.corsdomain '*' \
      --ws --ws.addr 0.0.0.0 --ws.port $wsport --ws.api eth,net,web3 --ws.origins '*'  \
      --verbosity 3  >> $node/node.log 2>&1 &

      sleep 3s;
      ps -ef|grep geth|grep mine|grep -v grep;
      ```
   You may need to change the P2P/HTTP/RPC/WS ports to avoid conflicts. Please,
   remember to change the `--nat` flag's extip if you want other nodes to be able
   to find yours. Refer to
   https://geth.ethereum.org/docs/fundamentals/command-line-options for more
   details about start options.
   
   This script expects node DB directory to be `./node` and the address of
   your account to be stored at `./node/node_address.txt`.
   
   Once all necessary flags are applied to the `./geth` command, you may run the
   seed node by running this script.

   2. **Running a consensus node.** Consensus node is a network member that takes
      part in a block acceptance process. Every node is allowed take part in the
      consensus process in watch-only mode. However, to become a block-producing
      validator the candidate must register itself via Governance contract and earn
      enough votes from the Neo X users. Please refer to the
      [Neo X Governance documentation](https://xdocs.ngd.network/governance/governance-in-neo-x)
      and to the [Neo X candidate registration documentation](https://xdocs.ngd.network/development/running-a-neo-x-node#id-4.b.5.-registering-as-a-candidate)
      for more details.
      
      To start a consensus node use `--mine` flag with addition to the script for the
      seed node. The following script may be used as a template:
      ```shell
      #!/bin/bash
      node="./node"
      miner=$(<$node/node_address.txt)
      ​
      port=30303
      udpport=30303
      httpport=8551
      rpcport=8561
      wsport=8571
      ​
      nohup ./geth \
      --networkid 47763 \
      --nat extip:127.0.0.1 \
      --port $port \
      --discovery.port $udpport \
      --mine --miner.pending.feeRecipient $miner \
      --unlock $miner \
      --password $node/password.txt \
      --antimev.password $node/password.txt \
      --dkg.one-msg-r1cs $node/r1cs/one_message.ccs \
      --dkg.two-msg-r1cs $node/r1cs/two_message.ccs \
      --dkg.seven-msg-r1cs $node/r1cs/seven_message.ccs \
      --dkg.one-msg-pk $node/pk/one_message.pk \
      --dkg.two-msg-pk $node/pk/two_message.pk \
      --dkg.seven-msg-pk $node/pk/seven_message.pk \
      --authrpc.port $rpcport \
      --identity $node \
      --maxpeers 50 \
      --syncmode full \
      --gcmode archive \
      --datadir $node \
      --bootnodes "enode://92eec46dd8b67ea8d8999defe0bf2b43d4c4802ed42a430843fec97dafbdc9128849261bdf1a940d431fc61f06a1317f5fc7c0386e18a9bbf951d0ccd8bf4f98@34.42.6.58:30303,enode://f289fb5c83ed39cf7d7aff2727afe70bf7951222c4a9aaef7bcbceef9fd0b53e4b6c9c0e08a50774dfd50d93e83b977932e4780934d379a6a0ac10cc44c6cfdb@34.87.188.162:30303" \
      --verbosity 3  >> $node/node.log 2>&1 &

      sleep 3s;
      ps -ef|grep geth|grep mine|grep -v grep;
      ```
      You may need to change the P2P/HTTP/RPC/WS ports to avoid conflicts. Please,
      remember to change the `--nat` flag's extip if you want other nodes to be able
      to find yours. Refer to
      https://geth.ethereum.org/docs/fundamentals/command-line-options for more
      details about start options.

      This script expects node DB directory to be `./node`, the address of
      the consensus node account to be stored at `./node/node_address.txt` and
      the password to be stored at `./node/password.txt`.
   
      Once all necessary flags are applied to the `./geth` command, you may run the
      seed node by running this script.

8. You may also start the built-in interactive
   [JavaScript console](https://geth.ethereum.org/docs/interacting-with-geth/javascript-console),
   (via the trailing `console` subcommand) through which you can interact using
   [`web3` methods](https://github.com/ChainSafe/web3.js/blob/0.20.7/DOCUMENTATION.md) 
   (note: the `web3` version bundled within `geth` is very old, and not up to date
   with official docs), as well as `geth`'s own [management APIs](https://geth.ethereum.org/docs/interacting-with-geth/rpc).
   This tool is optional and if you leave it out you can always attach it to an
   already running `geth` instance with `geth attach`.

*Note: Although some internal protective measures prevent transactions from
crossing over between the main network and test network, you should always
use separate accounts for play and real money. Unless you manually move
accounts, `geth` will by default correctly separate the two networks and will not make any
accounts available between them.*

### Configuration

As an alternative to passing the numerous flags to the `geth` binary, you can also pass a
configuration file via:

```shell
$ geth --config /path/to/your_config.toml
```

To get an idea of how the file should look like you can use the `dumpconfig` subcommand to
export your existing configuration:

```shell
$ geth --your-favourite-flags dumpconfig
```

#### Docker quick start

One of the quickest ways to get Ethereum up and running on your machine is by using
Docker:

```shell
docker run -d --name ethereum-node -v /Users/alice/ethereum:/root \
           -p 8545:8545 -p 30303:30303 \
           ethereum/client-go
```

This will start `geth` in snap-sync mode with a DB memory allowance of 1GB, as the
above command does.  It will also create a persistent volume in your home directory for
saving your blockchain as well as map the default ports. There is also an `alpine` tag
available for a slim version of the image.

Do not forget `--http.addr 0.0.0.0`, if you want to access RPC from other containers
and/or hosts. By default, `geth` binds to the local interface and RPC endpoints are not
accessible from the outside.

### Programmatically interfacing `geth` nodes

As a developer, sooner rather than later you'll want to start interacting with `geth` and the
Ethereum network via your own programs and not manually through the console. To aid
this, `geth` has built-in support for a JSON-RPC based APIs ([standard APIs](https://ethereum.org/en/developers/docs/apis/json-rpc/)
and [`geth` specific APIs](https://geth.ethereum.org/docs/interacting-with-geth/rpc)).
These can be exposed via HTTP, WebSockets and IPC (UNIX sockets on UNIX based
platforms, and named pipes on Windows).

The IPC interface is enabled by default and exposes all the APIs supported by `geth`,
whereas the HTTP and WS interfaces need to manually be enabled and only expose a
subset of APIs due to security reasons. These can be turned on/off and configured as
you'd expect.

HTTP based JSON-RPC API options:

  * `--http` Enable the HTTP-RPC server
  * `--http.addr` HTTP-RPC server listening interface (default: `localhost`)
  * `--http.port` HTTP-RPC server listening port (default: `8545`)
  * `--http.api` API's offered over the HTTP-RPC interface (default: `eth,net,web3`)
  * `--http.corsdomain` Comma separated list of domains from which to accept cross-origin requests (browser enforced)
  * `--ws` Enable the WS-RPC server
  * `--ws.addr` WS-RPC server listening interface (default: `localhost`)
  * `--ws.port` WS-RPC server listening port (default: `8546`)
  * `--ws.api` API's offered over the WS-RPC interface (default: `eth,net,web3`)
  * `--ws.origins` Origins from which to accept WebSocket requests
  * `--ipcdisable` Disable the IPC-RPC server
  * `--ipcpath` Filename for IPC socket/pipe within the datadir (explicit paths escape it)

You'll need to use your own programming environments' capabilities (libraries, tools, etc) to
connect via HTTP, WS or IPC to a `geth` node configured with the above flags and you'll
need to speak [JSON-RPC](https://www.jsonrpc.org/specification) on all transports. You
can reuse the same connection for multiple requests!

**Note: Please understand the security implications of opening up an HTTP/WS based
transport before doing so! Hackers on the internet are actively trying to subvert
Ethereum nodes with exposed APIs! Further, all browser tabs can access locally
running web servers, so malicious web pages could try to subvert locally available
APIs!**

### Operating a private network

Maintaining your own private network is more involved as a lot of configurations taken for
granted in the official networks need to be manually set up.

It is still easy to set up a network of Neo X nodes without heavy ZK computations.

There are two different solutions depending on your preference:

  * If you are looking for an eazy-to-operate test network, you can set one up with any of
    the three following commands.
    ```shell
    make privnet_start
    make privnet_start_four
    make privnet_start_seven
    ```
  * If you prefer to play with Docker, you can set one up with any of the three following
    commands.
    ```shell
    make docker_privnet_start
    make docker_privnet_start_four
    make docker_privnet_start_seven
    ```

## Contribution

Thank you for considering helping out with the source code! We welcome contributions
from anyone on the internet, and are grateful for even the smallest of fixes!

If you'd like to contribute to go-ethereum, please fork, fix, commit and send a pull request
for the maintainers to review and merge into the main code base. If you wish to submit
more complex changes though, please check up with the core devs first on [our Discord Server](https://discord.gg/invite/nthXNEv)
to ensure those changes are in line with the general philosophy of the project and/or get
some early feedback which can make both your efforts much lighter as well as our review
and merge procedures quick and simple.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting)
   guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary)
   guidelines.
 * Pull requests need to be based on and opened against the `master` branch.
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "eth, rpc: make trace configs optional"

Please see the [Developers' Guide](https://geth.ethereum.org/docs/developers/geth-developer/dev-guide)
for more details on configuring your environment, managing project dependencies, and
testing procedures.

### Contributing to geth.ethereum.org

For contributions to the [go-ethereum website](https://geth.ethereum.org), please checkout and raise pull requests against the `website` branch.
For more detailed instructions please see the `website` branch [README](https://github.com/ethereum/go-ethereum/tree/website#readme) or the 
[contributing](https://geth.ethereum.org/docs/developers/geth-developer/contributing) page of the website.

## License

The go-ethereum library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html),
also included in our repository in the `COPYING.LESSER` file.

The go-ethereum binaries (i.e. all code inside of the `cmd` directory) are licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also
included in our repository in the `COPYING` file.
