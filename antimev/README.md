# Neo X Anti-MEV

The anti-mev feature of Neo X is built based-on decentralized key generation, threshold encryption and enhanced dBFT consensus algorithm.

Users can encrypt and send their secret transactions through Envelopes to avoid malicious MEV attacks from miners or other senders in public transaction pool.

## DKG

Neo X empowers a fully decentralized key generation among consensus members. After being elected by Neo X Governance, seven candidates will be scheduled to become the next consensus group of dBFT. But before the formal epoch change, a successful round of DKG is required.

During each round of DKG, there are 3 things required.

1. The next consensus group performs a `Share` to generate `n` distributed secret shares and a global public key, where `n` is the size of Neo X consensus;
2. The current consensus group (if exists) performs a `Reshare` to pass the secret of the last round to this round;
3. (optional) If no more than `f` shares of the last round secret are lost, then left `2f+1` shares perform a `Recover` to complete Step 2.

Since [v0.3.0](https://github.com/bane-labs/go-ethereum/releases/tag/v0.3.0), the DKG module of consensus engine handles the whole process automatically, except setting up the initial anti-mev keystore with a secret passphrase.

### Share

Every participant in `Share` should execute the following steps.

1. Take a random polynomial $f(x)=a_0+a_1 x+a_2 x^2+⋯a_{t-1}x^{t-1}$ as his local secret, where `t` is the threshold, should equal to `2f+1`;
2. Compute $f_1,f_2,...,f_n$ where $f_i=f(i)$ and share them with corresponding participants, where `i` is the index of different participants of `Share`;
3. Accept all $f_i$ from other participants as $f_1(i),f_2(i),...,f_n(i)$, where `i` is the index of receiver, and compute $s_i=\sum f_i$ to get the final secret key.

Meanwhile, the global public key is generated with following steps.

1. Every participant uploads $F(x)=f(x)G_1$ within his PVSS to the KeyManagement contract;
2. KeyManagement contract verifies the validity of each PVSS, and compute $S=\sum_{i=1}^n F_i(0)$ as the global public key;

A well-constructed PVSS should contain following properties.

1. $F(x)=f(x)G_1$ as the commitment of sender's local secret;
2. $rG_1,rG_2$ as a pair of commitment of a random scalar $r$;
3. $F=(F(1),F(2),...,F(n))$ as the commitment share messages;

The KeyManagement verifies the computation of $F(1),F(2),...,F(n)$, and the validity of scalar $r$. Receivers then verify the commitment against the received share message through $e(r_1f(i),g_2)=e(F(i),r_2)$.

To ensure a verifiable message encryption of $f(i)$, zero-knowledge proof will be adopted in the near future.

### Reshare

Every participant in `Reshare` should execute following steps.

1. Regenerate his local secret as $f'(x)=a_0+a'_1 x+a'_2 x^2+⋯a'_{t-1}x^{t-1}$, only keep the same $a_0$;
2. Repeat `Share` Step 2 and 3, but send the sharings to the next consensus group.

The KeyManagement contract ensures each $F(0)=F'(0)$, so that after `Reshare`, the global public key doesn't change, and the original local secrets and secret shares are not leaked.

### Recover

The current consensus group in `Recover` should send all received sharings $f_i$ from lost index `i` to its successor. So that the receiver can recover the original local secrets through [Lagrange interpolation](https://en.wikipedia.org/wiki/Lagrange_polynomial).

`Recover` exposes some (no more than `f`) of the original local secrets, hence `Recover` stage is not allowed until index `i` is confirmed to be absent from `Reshare`.

## TPKE

The DKG procedure held by Governance allows a threshold public key encryption (TPKE) scheme on Neo X, as well as a threshold block signature scheme. Only if at least `2f+1` of Neo X CNs broadcast a piece of a secret/signature, the network can decrypt a transaction or sign a valid block.

Neo X TPKE encodes any secret to BLS12381 $G_1$ for encryption and any message to BLS12381 $G_2$ for signature.

### Encryption

For a secret $msg$, Neo X takes a random $G_1$ point $P$ as the seed for a random AES key, so its ciphertext is $C_1=AES(Hash(P), msg)$.

Neo X also takes a random scalar $r$ to encrypt $P$ as $C_2=P+rS$, where $r$ is a random scalar and $S$ is the global public key. The final encrypted message broadcasted in the network is then $C=(C_1,C_2)$.

To recover the original $msg$, the Neo X consensus needs to decrypt $C_1$ to recover $P$. Each CN should try to compute and share $s_iR$, where $R$ is the commitment of the random scalar $r$ and $s_i$ is the local secret key.

Since the validator index (DKG index) is public in Neo X Governance, these shares then can be aggregated and solved with [Vandermonde matrix](https://en.wikipedia.org/wiki/Vandermonde_matrix).

After recovering the seed $P$, everyone in the network can compute the original $msg$ through AES decryption.

### Signature

For a message $msg$, Neo X encodes it to $G_2$ as $Q=HashToG2(msg)$, then a signature share can be computed as $s_iH$, where $s_i$ is the local secret key.

After collecting enough broadcasted shares, CNs can aggregate and get the final signature with [Vandermonde matrix](https://en.wikipedia.org/wiki/Vandermonde_matrix) in the same way as TPKE decryption.

## Envelope Transaction

Neo X users can only send secret transactions to prevent malicious MEV attacks through Envelope transactions.

### Construction

Only transactions have the following properties can be acknowledged as valid Envelopes in dBFT.

1. The `to` address is Neo X GovReward contract (`0x1212000000000000000000000000000000000003`);
2. The `from` address is the same as the inner secret transaction;
3. The `nonce` is the same as the inner secret transaction;
4. The `gastip` is not lower than Neo X `minGasTipCap` policy plus `envelopeFee` policy, and the inner transaction `gastip` is not lower than this value;
5. The `data` begins with `0xffffffff`, then is followed by a 4-byte DKG epoch index (in BE form), then is followed by a 4-byte inner secret transaction `gaslimit` (in BE form), then is followed by a 32-byte inner secret transaction hash, then is followed by an encoded TPKE ciphertext.

So in a nutshell, Envelopes are always calling the `fallback()` method of Neo X GovReward contract. This method will burn gas based on declared `gaslimit` in `data` to allocate block space in Envelope execution, and it works with `eth_estimateGas` automatically.

Here is an example of the `data` field of an Envelope transaction.
```
|  prefix  | epoch  |gaslimit|                 inner secret transaction hash                  |  TPKE ciphertext  |
|  4-byte  | 4-byte | 4-byte |                            32-byte                             |     left bytes    |
|0xffffffff|00000001|00005208|777bbe0bb1e4c34310c50160c1a22e66bfab8531c54b9bc87739a17eff6fd15a|80f8c8c2...fa6a1810|
```

To send a secret transaction wrapped with an Envelope, we recommend the following steps which should be compatible with most of popular wallets (e.g. Metamask).

1. Construct a secret transaction;
2. Request the wallet to sign this transaction and send it to nodes configured with `--txpool.amevcache`;
3. Request the wallet to sign the `nonce` of the secret transaction as a message;
4. Use this signature to fetch the signed transaction through `eth_getCachedTransaction`;
5. Encrypt the signed transaction with Neo X TPKE;
6. Construct an Envelope transaction with the encrypted data and send it through wallet with the same `nonce`.

It has to be mentioned that the nodes in Step 2 always return an RPC error to prevent the wallet nonce from increasing. Please try Step 3 and 4 to confirm the result or analyze the RPC error message.

### Verification

Envelopes should first pass the mempool verification, otherwise they can't get further decrypted in dBFT. 

1. The sender should have enough balance to pay for the block space which the Envelope requires;
2. The `nonce` used by both the secret transaction and its Envelope is valid;
3. The Envelope `gaslimit` should not exceed the `maxEnvelopeGasLimit` policy.

If a user sends a valid Envelope transaction, and the TPKE ciphertext can be decrypted successfully, and the decrypted inner transaction is valid, then the decrypted transaction will replace the Envelope transaction in the block space and get executed, regardless of failed or not.

If a user sends an invalid Envelope transaction, or the TPKE ciphertext can't be decrypted successfully, or the decrypted inner transaction is invalid, then the Envelope transaction will be rejected by mempool or get mined into the next block directly (if Envelope is accepted, then the amount of gas designated for inner transaction execution is burned). The latter means **the user will pay for the block space, but the secret transaction will not be acknowledged**.

It has to be mentioned that there are both gas limit and number limit for Envelope transactions in each block, which is defined by the Neo X `maxEnvelopesPerBlock` policy and `maxEnvelopeGasLimit` policy. So your Envelope transactions may get delayed in mempool when there is traffic.

### RPC API

There are several new and useful RPC APIs for Envelope construction.

1. `eth_getCachedTransaction` returns the cached and signed secret transactions. It requires a valid sender signature in parameters, and only works on nodes configured with `--txpool.amevcache`;
2. `eth_envelopeFee` returns the minimum additional `gastip`/`gasprice` that anti-mev transactions should pay for the service;
3. `eth_maxEnvelopeGasLimit` returns the maximum `gaslimit` that an Envelope can declare for itself.

## Anti-MEV dBFT

To preform an honest transaction ordering and prevent malicious MEV attacks, the dBFT consensus of Neo X has a new period `PreCommit`. Encrypted transactions wrapped with Envelopes are firstly ordered in `PrepareRequest` and `PrepareResponse`, then are decrypted during `PreCommit` stage and included into the proposed block instead of corresponding Envelopes. So that during the final `Commit` stage block signatures are collected for the finalized proposal that includes decrypted transactions.

It is the Envelope transactions that are first proposed in `PrepareRequest`. They are handled in the same way as normal transactions, so Envelopes can appear anywhere in the block space. But since they always pay additional decryption fee in price, most of them will be placed at the front.

After `PrepareResponse`, dBFT has already confirmed a solid `PreBlock` for the next block height, so the transaction order is determined before Envelope decryption. Hence, in anti-mev dBFT, nobody can't audit the content of Envelopes and reorder these transactions based on MEV.

During the new `PreCommit` period, CNs pick out Envelope transactions and broadcast decryption shares for them. So that in the end of `PreCommit` there must be at least `2f+1` decryption shares and Envelope transactions can be replaced by decrypted inner transactions. View change will not happen after here, so that it's impossible for secret transactions to become a target of MEV attack after they got decrypted.

In the final `Commit`, CNs compute and broadcast signature shares for the new block proposal. Only after at least `2f+1` signature shares are collected, the new block can have a valid signature and get acknowledged by the network.
