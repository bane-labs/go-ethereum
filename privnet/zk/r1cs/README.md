# R1CS

We use [Consensys/gnark](https://github.com/Consensys/gnark) for ZK proving, and the circuit implementation can be found at [bane-labs/zk-dkg](https://github.com/bane-labs/zk-dkg).

Now this privnet uses the same MPC ceremony as in production. You can download the required files from the following links.

- https://zkstorage.blob.core.windows.net/zk-blob/one_message.ccs
- https://zkstorage.blob.core.windows.net/zk-blob/two_message.ccs
- https://zkstorage.blob.core.windows.net/zk-blob/seven_message.ccs

Or, if you're familiar with NeoFS, you can also download them as described in [bane-labs/mpc](https://github.com/bane-labs/mpc).

Please put the R1CS files in this folder, as defined in the Makefile.
