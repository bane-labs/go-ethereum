package core

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

type FileSystem struct {
	blobFeed event.Feed
}

func NewFileSystem() (*FileSystem, error) {
	return &FileSystem{}, nil
}

func (fs *FileSystem) GetSidecarsByRoot(hash common.Hash) types.BlobSidecars {
	return nil
}

func (fs *FileSystem) InsertBlobs(hash common.Hash, blobs types.BlobSidecars) error {
	return nil
}

func (fs *FileSystem) SubscribeBlobsEvent(ch chan<- BlobEvent) event.Subscription {
	return fs.blobFeed.Subscribe(ch)
}
