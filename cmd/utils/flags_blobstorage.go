package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/core/filesystem"
	"github.com/ethereum/go-ethereum/core/filesystem/primitives"
	"github.com/ethereum/go-ethereum/internal/flags"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

var (
	BlobStoragePathFlag = &cli.PathFlag{
		Name:     "blob-path",
		Usage:    "Location for blob storage. Default location will be a 'blobs' directory next to the datadir.",
		Category: flags.BlobStorageCategory,
	}
	BlobStorageLayout = &cli.StringFlag{
		Name:        "blob-storage-layout",
		Usage:       "Dictates how to organize the blob directory structure on disk, " + layoutOptions(),
		DefaultText: fmt.Sprintf("\"%s\", unless a different existing layout is detected", filesystem.LayoutNameByEpoch),
		Category:    flags.BlobStorageCategory,
	}
	BlobRetentionEpochFlag = &cli.Uint64Flag{
		Name:     "blob-retention-epochs",
		Usage:    "Override the default blob retention period (measured in epochs). The node will exit with an error at startup if the value is less than the default of 8192 epochs.",
		Value:    uint64(params.MinEthEpochsForBlobsSidecarsRequest),
		Aliases:  []string{"extend-blob-retention-epoch"},
		Category: flags.BlobStorageCategory,
	}
	// BlobSaveFsync enforces durable filesystem writes for use cases where blob availability is critical.
	BlobSaveFsync = &cli.BoolFlag{
		Name:     "blob-save-fsync",
		Usage:    "Forces new blob files to be fysnc'd before continuing, ensuring durable blob writes.",
		Category: flags.BlobStorageCategory,
	}
)

// BlobStorageFlags is the list of CLI flags for configuring blob storage.
var BlobStorageFlags = []cli.Flag{
	BlobStoragePathFlag,
	BlobStorageLayout,
	BlobRetentionEpochFlag,
	BlobSaveFsync,
}

func layoutOptions() string {
	return "available options are: " + strings.Join(filesystem.LayoutNames, ", ") + "."
}

func validateLayoutFlag(_ *cli.Context, v string) error {
	if slices.Contains(filesystem.LayoutNames, v) {
		return nil
	}
	return errors.Errorf("invalid value '%s' for flag --%s, %s", v, BlobStorageLayout.Name, layoutOptions())
}

// SetBlobStorageConfig sets the blob storage configuration options in the node config.
func SetBlobStorageConfig(ctx *cli.Context, cfg *node.Config) {
	blobRetentionEpoch, err := blobRetentionEpoch(ctx)
	if err != nil {
		Fatalf("%s", errors.Wrap(err, "blob retention epoch"))
	}

	blobPath := blobStoragePath(ctx)
	layout, err := detectLayout(blobPath, ctx)
	if err != nil {
		Fatalf("%s", errors.Wrap(err, "detecting blob storage layout"))
	}

	cfg.BlobRetentionEpoch = uint64(blobRetentionEpoch)
	cfg.BlobStoragePath = blobPath
	cfg.BlobStorageLayout = layout
	if ctx.IsSet(BlobSaveFsync.Name) {
		cfg.BlobSaveFsync = true
	}
}

// stringFlagGetter makes testing detectLayout easier
// because we don't need to mess with FlagSets and cli types.
type stringFlagGetter interface {
	String(name string) string
}

// detectLayout determines which layout to use based on explicit user flags or by probing the
// blob directory to determine the previously used layout.
// - explicit: If the user has specified a layout flag, that layout is returned.
// - flat: If directories that look like flat layout's block root paths are present.
// - by-epoch: default if neither of the above is true.
func detectLayout(dir string, c stringFlagGetter) (string, error) {
	explicit := c.String(BlobStorageLayout.Name)
	if explicit != "" {
		return explicit, nil
	}

	dir = filepath.Clean(dir)
	// nosec: this path is provided by the node operator via flag
	base, err := os.Open(dir) // #nosec G304
	if err != nil {
		// 'blobs' directory does not exist yet, so default to by-epoch.
		return filesystem.LayoutNameByEpoch, nil
	}
	defer func() {
		if err := base.Close(); err != nil {
			log.WithError(err).Errorf("Could not close blob storage directory")
		}
	}()

	// When we go looking for existing by-root directories, we only need to find one directory
	// but one of those directories could be the `by-epoch` layout's top-level directory,
	// and it seems possible that on some platforms we could get extra system directories that I don't
	// know how to anticipate (looking at you, Windows), so I picked 16 as a small number with a generous
	// amount of wiggle room to be confident that we'll likely see a by-root director if one exists.
	_, err = base.Readdirnames(16)
	if err != nil {
		// We can get this error if the directory exists and is empty
		if errors.Is(err, io.EOF) {
			return filesystem.LayoutNameByEpoch, nil
		}
		return "", errors.Wrap(err, "reading blob storage directory")
	}
	return filesystem.LayoutNameByEpoch, nil
}

func blobStoragePath(c *cli.Context) string {
	blobsPath := c.Path(BlobStoragePathFlag.Name)
	if blobsPath == "" {
		// append a "blobs" subdir to the end of the data dir path
		blobsPath = filepath.Join(c.String(DataDirFlag.Name), "blobs")
	}
	return blobsPath
}

var errInvalidBlobRetentionEpochs = errors.New("value is smaller than spec minimum")

// blobRetentionEpoch returns the spec default MIN_EPOCHS_FOR_BLOB_SIDECARS_REQUEST
// or a user-specified flag overriding this value. If a user-specified override is
// smaller than the spec default, an error will be returned.
func blobRetentionEpoch(cliCtx *cli.Context) (primitives.Epoch, error) {
	spec := primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest)
	if !cliCtx.IsSet(BlobRetentionEpochFlag.Name) {
		return spec, nil
	}

	re := primitives.Epoch(cliCtx.Uint64(BlobRetentionEpochFlag.Name))
	// Validate the epoch value against the spec default.
	if re < primitives.Epoch(params.MinEthEpochsForBlobsSidecarsRequest) {
		return spec, errors.Wrapf(errInvalidBlobRetentionEpochs, "%s=%d, spec=%d", BlobRetentionEpochFlag.Name, re, spec)
	}

	return re, nil
}

func init() {
	BlobStorageLayout.Action = validateLayoutFlag
}
