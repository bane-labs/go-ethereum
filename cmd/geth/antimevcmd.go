package main

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/antimev"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/console/prompt"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"
)

var (
	antimevCommand = &cli.Command{
		Name:  "antimev",
		Usage: "Manage antimev data",
		Description: `
		
Manage antimev file, display existing antimev keystore status, creates a new
keystore or update the existing keystore.

It supports interactive mode, when you are prompted for password as well as
non-interactive mode where passwords are supplied via a given password file.
Non-interactive mode is only meant for scripted use on test networks or known
safe environments.

Make sure you remember the password you gave when creating a new keystore.
Without it you are not able to unlock your antimev keystore.

Note that exporting your key or DKG secrets in unencrypted format is NOT
supported.

The file is stored as <DATADIR>/antimev-keystore.
It is safe to transfer the keystore file between ethereum nodes by simply
copying.

Make sure you are not accessing during DKG process, or reinitializing an
existing keystore file.`,
		Subcommands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "Print status of existing antimev keystore",
				Action: antimevStatus,
				Flags: []cli.Flag{
					utils.AntiMEVKeyStoreFlag,
					utils.AntiMEVPasswordFlag,
				},
				Description: `
Print a short summary of antimev keystore and DKG.

The keystore is saved in encrypted format, you are prompted for a password.

For non-interactive use, password can be specified with --antimev.password flag.
`,
			},
			{
				Name:   "init",
				Usage:  "Create a new antimev keystore",
				Action: antimevInit,
				Flags: []cli.Flag{
					utils.AntiMEVKeyStoreFlag,
					utils.AntiMEVPasswordFlag,
				},
				Description: `
	geth antimev init <address> <consensusSize>
	
Creates a new antimev keystore for <address> and prints the message public key.

DKG related parameter is generated based on <consensusSize>, default 7.

The keystore is saved in encrypted format, you are prompted for a password.

You must remember this password to unlock your keystore in the future.

For non-interactive use, password can be specified with --antimev.password flag:

Note, this is meant to be used for testing only, it is a bad idea to save your
password to file or expose in any other way.
`,
			},
			{
				Name:   "update",
				Usage:  "Update an existing antimev keystore",
				Action: antimevUpdate,
				Flags: []cli.Flag{
					utils.AntiMEVKeyStoreFlag,
					utils.AntiMEVPasswordFlag,
				},
				Description: `
    geth antimev update

Update the existing antimev keystore.

The keystore is saved in the newest version in encrypted format, you are prompted
for a password to unlock the keystore and another to save the updated file.

For non-interactive use, password can be specified with --antimev.password flag:

    geth antimev update [options]

Since only one password can be given, only format update can be performed,
changing your password is only possible interactively.
`,
			},
			{
				Name:   "reset",
				Usage:  "Reset an existing antimev keystore",
				Action: antimevReset,
				Flags: []cli.Flag{
					utils.AntiMEVKeyStoreFlag,
					utils.AntiMEVPasswordFlag,
				},
				Description: `
    geth antimev reset

Reset the existing antimev keystore.

The keystore is reset to the initial state before any DKG round, but the message
key, identity address and basic DKG parameters are reserved.

For non-interactive use, password can be specified with --antimev.password flag:

    geth antimev reset [options]

Note, you can use this command to recycle your keystore for Privnet or other
networks, but it is a bad idea to reset an in-use one when everything runs
correctly.
`,
			},
		},
	}
)

// makeAntimevKeystore creates an antimev keystore with node config
func makeAntimevKeystore(ctx *cli.Context) *antimev.KeyStore {
	cfg := loadBaseConfig(ctx)
	path, err := cfg.Node.GetAntiMEVKeyStorePath()
	if err != nil {
		utils.Fatalf("Failed to get the keystore path: %v", err)
	}
	return antimev.NewKeyStore(path)
}

// tries unlocking the antimev keystore a few times.
func unlockKeyStore(ks *antimev.KeyStore, passwords []string) error {
	var err error
	for trials := 0; trials < 3; trials++ {
		prompt := fmt.Sprintf("Unlocking antimev keystore | Attempt %d/%d", trials+1, 3)
		password := utils.GetPassPhraseWithList(prompt, false, 0, passwords)
		err = ks.Load(password)
		if err == nil {
			log.Info("Unlocked antimev keystore", "identity", ks.Address())
			return nil
		}
		if err != antimev.ErrKeystoreDecryption {
			// No need to prompt again if the error is not decryption-related.
			break
		}
	}
	// All trials expended to unlock account, bail out
	utils.Fatalf("Failed to unlock antimev keystore (%v)", err)
	return nil
}

// antimevStatus prints the status of specified antimev keystore
func antimevStatus(ctx *cli.Context) error {
	ks := makeAntimevKeystore(ctx)
	unlockKeyStore(ks, utils.MakeAntiMEVPasswordList(ctx))
	var cpkStr, lpkStr string
	cpk, err := ks.GlobalPublicKey()
	if err == nil {
		cpkStr = hex.EncodeToString(cpk.Bytes())
	}
	lpk, err := ks.LastGlobalPublicKey()
	if err == nil {
		lpkStr = hex.EncodeToString(lpk.Bytes())
	}
	fmt.Printf("Antimev keystore status:\n")
	fmt.Printf("- Message public key: {%s}\n", ks.MessagePubKey())
	fmt.Printf("- Successful rounds: {%d}\n", ks.Round())
	fmt.Printf("- Resharing: {%t}\n", ks.IsResharing())
	fmt.Printf("- Sharing: {%t}\n", ks.IsSharing())
	fmt.Printf("- Current global key: {%s}\n", cpkStr)
	fmt.Printf("- Last global key: {%s}\n", lpkStr)
	return nil
}

// antimevStatus creates a new antimev keystore
func antimevInit(ctx *cli.Context) error {
	// Get an address as DKG identity
	if ctx.Args().Len() < 1 {
		utils.Fatalf("No account specified to init for")
	}
	addr := ctx.Args().First()
	// Get the size and threshold for keystore initialization
	size := 7
	if ctx.Args().Len() == 2 {
		n, err := strconv.ParseInt(ctx.Args().Get(1), 10, 64)
		if err != nil {
			utils.Fatalf("Init error in parsing parameters: consensus size not an integer\n")
		}
		size = int(n)
	}
	threshold := size - (size-1)/3
	// Generate a secp256k1 keypair for message encryption and decryption
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)
	key, _ := ecies.GenerateKey(random, crypto.S256(), nil)
	// Init a new antimev keystore
	ks := makeAntimevKeystore(ctx)
	password := utils.GetPassPhraseWithList("Your new keystore is locked with a password. Please give a password. Do not forget this password.", true, 0, utils.MakeAntiMEVPasswordList(ctx))
	err := ks.Init(common.HexToAddress(addr), key, size, threshold, password)
	if err != nil {
		utils.Fatalf("Failed to init antimev keystore: %v", err)
	}
	err = ks.Persist()
	if err != nil {
		utils.Fatalf("Failed to persist antimev keystore: %v", err)
	}
	path, err := ks.Path()
	if err != nil {
		utils.Fatalf("Failed to get antimev keystore path: %v", err)
	}
	fmt.Printf("\nYour new antimev keystore was generated\n\n")
	fmt.Printf("Message public key of the keystore: %s\n", ks.MessagePubKey())
	fmt.Printf("Path of the keystore file:          %s\n\n", path)
	fmt.Printf("- You must NEVER share the keystore file with anyone!\n")
	fmt.Printf("- You must REMEMBER your password! Without the password, it's impossible to decrypt the key!\n\n")
	return nil
}

// antimevUpdate provides the possibility to change the pass-phrase.
func antimevUpdate(ctx *cli.Context) error {
	ks := makeAntimevKeystore(ctx)
	unlockKeyStore(ks, utils.MakeAntiMEVPasswordList(ctx))
	newPassword := utils.GetPassPhraseWithList("Please give a new password. Do not forget this password.", true, 0, nil)
	ks.UpdatePassword(newPassword)
	if err := ks.Persist(); err != nil {
		utils.Fatalf("Could not update the keystore: %v", err)
	}
	return nil
}

// antimevReset cleans all DKG data but keep the message keypair and other parameters.
func antimevReset(ctx *cli.Context) error {
	ks := makeAntimevKeystore(ctx)
	path, err := ks.Path()
	if err != nil {
		utils.Fatalf("Could not reset the keystore: %v", err)
	}
	confirm, err := prompt.Stdin.PromptConfirm(fmt.Sprintf("Reset '%s'?", path))
	if err != nil {
		utils.Fatalf("%v", err)
	}
	if confirm {
		unlockKeyStore(ks, utils.MakeAntiMEVPasswordList(ctx))
		// Reset the keystore to the oldest possible state before any DKG
		ks.Reset(0)
		if err := ks.Persist(); err != nil {
			utils.Fatalf("Could not reset the keystore: %v", err)
		}
	}
	return nil
}
