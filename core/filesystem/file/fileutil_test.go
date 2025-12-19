// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.
package file_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/filesystem/file"
	"github.com/stretchr/testify/require"
)

func TestPathExpansion(t *testing.T) {
	u, err := user.Current()
	require.NoError(t, err)
	tests := map[string]string{
		"/home/someuser/tmp": "/home/someuser/tmp",
		"~/tmp":              u.HomeDir + "/tmp",
		"$DDDXXX/a/b":        "/tmp/a/b",
		"/a/b/":              "/a/b",
	}
	require.NoError(t, os.Setenv("DDDXXX", "/tmp"))
	for test, expected := range tests {
		expanded, err := file.ExpandPath(test)
		require.NoError(t, err)
		require.Equal(t, expected, expanded)
	}
}

func TestMkdirAll_AlreadyExists_Override(t *testing.T) {
	dirName := t.TempDir() + "somedir"
	err := os.MkdirAll(dirName, file.DefaultIoConfig().ReadWriteExecutePermissions)
	require.NoError(t, err)
	require.NoError(t, file.MkdirAll(dirName))
}

func TestMkdirAll_OK(t *testing.T) {
	dirName := t.TempDir() + "somedir"
	err := file.MkdirAll(dirName)
	require.NoError(t, err)
	exists, err := file.HasDir(dirName)
	require.NoError(t, err)
	require.Equal(t, true, exists)
}

func TestWriteFile_AlreadyExists_WrongPermissions(t *testing.T) {
	dirName := t.TempDir() + "somedir"
	err := os.MkdirAll(dirName, os.ModePerm)
	require.NoError(t, err)
	someFileName := filepath.Join(dirName, "somefile.txt")
	require.NoError(t, os.WriteFile(someFileName, []byte("hi"), os.ModePerm))
	err = file.WriteFile(someFileName, []byte("hi"))
	require.ErrorContains(t, err, "already exists without proper 0600 permissions")
}

func TestWriteFile_AlreadyExists_OK(t *testing.T) {
	dirName := t.TempDir() + "somedir"
	err := os.MkdirAll(dirName, os.ModePerm)
	require.NoError(t, err)
	someFileName := filepath.Join(dirName, "somefile.txt")
	require.NoError(t, os.WriteFile(someFileName, []byte("hi"), file.DefaultIoConfig().ReadWritePermissions))
	require.NoError(t, file.WriteFile(someFileName, []byte("hi")))
}

func TestWriteFile_OK(t *testing.T) {
	dirName := t.TempDir() + "somedir"
	err := os.MkdirAll(dirName, os.ModePerm)
	require.NoError(t, err)
	someFileName := filepath.Join(dirName, "somefile.txt")
	require.NoError(t, file.WriteFile(someFileName, []byte("hi")))
	exists, err := file.Exists(someFileName, file.Regular)
	require.NoError(t, err, "could not check if file exists")
	require.Equal(t, true, exists, "file does not exist")
}

func TestCopyFile(t *testing.T) {
	fName := t.TempDir() + "testfile"
	err := os.WriteFile(fName, []byte{1, 2, 3}, file.DefaultIoConfig().ReadWritePermissions)
	require.NoError(t, err)

	err = file.CopyFile(fName, fName+"copy")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, os.Remove(fName+"copy"))
	}()

	require.Equal(t, true, deepCompare(t, fName, fName+"copy"))
}

func TestCopyDir(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := filepath.Join(t.TempDir(), "copyfolder")
	type fileDesc struct {
		path    string
		content []byte
	}
	fds := []fileDesc{
		{
			path:    "testfile1",
			content: []byte{1, 2, 3},
		},
		{
			path:    "subfolder1/testfile1",
			content: []byte{4, 5, 6},
		},
		{
			path:    "subfolder1/testfile2",
			content: []byte{7, 8, 9},
		},
		{
			path:    "subfolder2/testfile1",
			content: []byte{10, 11, 12},
		},
		{
			path:    "testfile2",
			content: []byte{13, 14, 15},
		},
	}
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir1, "subfolder1"), 0777))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir1, "subfolder2"), 0777))
	for _, fd := range fds {
		require.NoError(t, file.WriteFile(filepath.Join(tmpDir1, fd.path), fd.content))

		exists, err := file.Exists(filepath.Join(tmpDir1, fd.path), file.Regular)
		require.NoError(t, err, "could not check if file exists")
		require.Equal(t, true, exists, "file does not exist")

		exists, err = file.Exists(filepath.Join(tmpDir2, fd.path), file.Regular)
		require.NoError(t, err, "could not check if file exists")
		require.Equal(t, false, exists, "file does exist")
	}

	// Make sure that files are copied into non-existent directory only. If directory exists function exits.
	require.ErrorContains(t, file.CopyDir(tmpDir1, t.TempDir()), "destination directory already exists")
	require.NoError(t, file.CopyDir(tmpDir1, tmpDir2))

	// Now, all files should have been copied.
	for _, fd := range fds {
		exists, err := file.Exists(filepath.Join(tmpDir2, fd.path), file.Regular)
		require.NoError(t, err, "could not check if file exists")
		require.Equal(t, true, exists)
		require.Equal(t, true, deepCompare(t, filepath.Join(tmpDir1, fd.path), filepath.Join(tmpDir2, fd.path)))
	}
	require.Equal(t, true, file.DirsEqual(tmpDir1, tmpDir2))
}

func TestDirsEqual(t *testing.T) {
	t.Run("non-existent source directory", func(t *testing.T) {
		require.Equal(t, false, file.DirsEqual(filepath.Join(t.TempDir(), "nonexistent"), t.TempDir()))
	})

	t.Run("non-existent dest directory", func(t *testing.T) {
		require.Equal(t, false, file.DirsEqual(t.TempDir(), filepath.Join(t.TempDir(), "nonexistent")))
	})

	t.Run("non-empty directory", func(t *testing.T) {
		// Start with directories that do not have the same contents.
		tmpDir1, tmpFileNames := tmpDirWithContents(t)
		tmpDir2 := filepath.Join(t.TempDir(), "newfolder")
		require.Equal(t, false, file.DirsEqual(tmpDir1, tmpDir2))

		// Copy dir, and retest (hashes should match now).
		require.NoError(t, file.CopyDir(tmpDir1, tmpDir2))
		require.Equal(t, true, file.DirsEqual(tmpDir1, tmpDir2))

		// Tamper the data, make sure that hashes do not match anymore.
		require.NoError(t, os.Remove(filepath.Join(tmpDir1, tmpFileNames[2])))
		require.Equal(t, false, file.DirsEqual(tmpDir1, tmpDir2))
	})
}

func TestHashDir(t *testing.T) {
	t.Run("non-existent directory", func(t *testing.T) {
		hash, err := file.HashDir(filepath.Join(t.TempDir(), "nonexistent"))
		require.ErrorContains(t, err, "no such file or directory")
		require.Equal(t, "", hash)
	})

	t.Run("empty directory", func(t *testing.T) {
		hash, err := file.HashDir(t.TempDir())
		require.NoError(t, err)
		require.Equal(t, "hashdir:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=", hash)
	})

	t.Run("non-empty directory", func(t *testing.T) {
		tmpDir, _ := tmpDirWithContents(t)
		hash, err := file.HashDir(tmpDir)
		require.NoError(t, err)
		require.Equal(t, "hashdir:oSp9wRacwTIrnbgJWcwTvihHfv4B2zRbLYa0GZ7DDk0=", hash)
	})
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "testfile")
	nonExistentTmpFile := filepath.Join(tmpDir, "nonexistent")
	_, err := os.Create(tmpFile)
	require.NoError(t, err, "could not create test file")

	tests := []struct {
		name     string
		itemPath string
		itemType file.ObjType
		want     bool
	}{
		{
			name:     "file exists",
			itemPath: tmpFile,
			itemType: file.Regular,
			want:     true,
		},
		{
			name:     "dir exists",
			itemPath: tmpDir,
			itemType: file.Directory,
			want:     true,
		},
		{
			name:     "non-existent file",
			itemPath: nonExistentTmpFile,
			itemType: file.Regular,
			want:     false,
		},
		{
			name:     "non-existent dir",
			itemPath: nonExistentTmpFile,
			itemType: file.Directory,
			want:     false,
		},
		{
			name:     "file is dir",
			itemPath: tmpDir,
			itemType: file.Regular,
			want:     false,
		},
		{
			name:     "dir is file",
			itemPath: tmpFile,
			itemType: file.Directory,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := file.Exists(tt.itemPath, tt.itemType)
			require.NoError(t, err, "could not check if file exists")
			require.Equal(t, tt.want, exists)
		})
	}
}

func TestHashFile(t *testing.T) {
	originalData := []byte("test data")
	originalChecksum := sha256.Sum256(originalData)

	tempDir := t.TempDir()
	tempfile, err := os.CreateTemp(tempDir, "testfile")
	require.NoError(t, err)
	_, err = tempfile.Write(originalData)
	require.NoError(t, err)
	err = tempfile.Close()
	require.NoError(t, err)

	// Calculate the checksum of the temporary file
	checksum, err := file.HashFile(tempfile.Name())
	require.NoError(t, err)

	// Ensure the calculated checksum matches the original checksum
	require.Equal(t, hex.EncodeToString(originalChecksum[:]), hex.EncodeToString(checksum))
}

func TestDirFiles(t *testing.T) {
	tmpDir, tmpDirFnames := tmpDirWithContents(t)
	tests := []struct {
		name     string
		path     string
		outFiles []string
	}{
		{
			name:     "dot path",
			path:     filepath.Join(tmpDir, "/./"),
			outFiles: tmpDirFnames,
		},
		{
			name:     "non-empty folder",
			path:     tmpDir,
			outFiles: tmpDirFnames,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outFiles, err := file.DirFiles(tt.path)
			require.NoError(t, err)

			sort.Strings(outFiles)
			require.Equal(t, tt.outFiles, outFiles)
		})
	}
}

func TestRecursiveFileFind(t *testing.T) {
	tmpDir, _ := tmpDirWithContentsForRecursiveFind(t)
	/*
		 tmpDir
		 ├── file3
		 ├── subfolder1
		 │   └── subfolder11
		 │       └── file1
		 └── subfolder2
			 └── file2
	*/
	tests := []struct {
		name  string
		root  string
		found bool
	}{
		{
			name:  "file1",
			root:  tmpDir,
			found: true,
		},
		{
			name:  "file2",
			root:  tmpDir,
			found: true,
		},
		{
			name:  "file1",
			root:  tmpDir + "/subfolder1",
			found: true,
		},
		{
			name:  "file3",
			root:  tmpDir,
			found: true,
		},
		{
			name:  "file4",
			root:  tmpDir,
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, _, err := file.RecursiveFileFind(tt.name, tt.root)
			require.NoError(t, err)

			require.Equal(t, tt.found, found)
		})
	}
}

func TestRecursiveDirFind(t *testing.T) {
	tmpDir, _ := tmpDirWithContentsForRecursiveFind(t)

	/*
		 tmpDir
		 ├── file3
		 ├── subfolder1
		 │   └── subfolder11
		 │       └── file1
		 └── subfolder2
			 └── file2
	*/

	tests := []struct {
		name  string
		root  string
		found bool
	}{
		{
			name:  "subfolder11",
			root:  tmpDir,
			found: true,
		},
		{
			name:  "subfolder2",
			root:  tmpDir,
			found: true,
		},
		{
			name:  "subfolder11",
			root:  tmpDir + "/subfolder1",
			found: true,
		},
		{
			name:  "file3",
			root:  tmpDir,
			found: false,
		},
		{
			name:  "file4",
			root:  tmpDir,
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, _, err := file.RecursiveDirFind(tt.name, tt.root)
			require.NoError(t, err)

			require.Equal(t, tt.found, found)
		})
	}
}

func deepCompare(t *testing.T, file1, file2 string) bool {
	sf, err := os.Open(file1)
	require.NoError(t, err)
	df, err := os.Open(file2)
	require.NoError(t, err)
	sscan := bufio.NewScanner(sf)
	dscan := bufio.NewScanner(df)

	for sscan.Scan() && dscan.Scan() {
		if !bytes.Equal(sscan.Bytes(), dscan.Bytes()) {
			return false
		}
	}
	return true
}

// tmpDirWithContents returns path to temporary directory having some folders/files in it.
// Directory is automatically removed by internal testing cleanup methods.
func tmpDirWithContents(t *testing.T) (string, []string) {
	dir := t.TempDir()
	fnames := []string{
		"file1",
		"file2",
		"subfolder1/file1",
		"subfolder1/file2",
		"subfolder1/subfolder11/file1",
		"subfolder1/subfolder11/file2",
		"subfolder1/subfolder12/file1",
		"subfolder1/subfolder12/file2",
		"subfolder2/file1",
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subfolder1", "subfolder11"), 0777))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subfolder1", "subfolder12"), 0777))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subfolder2"), 0777))
	for _, fname := range fnames {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fname), []byte(fname), 0777))
	}
	sort.Strings(fnames)
	return dir, fnames
}

// tmpDirWithContentsForRecursiveFind returns path to temporary directory having some folders/files in it.
// Directory is automatically removed by internal testing cleanup methods.
func tmpDirWithContentsForRecursiveFind(t *testing.T) (string, []string) {
	dir := t.TempDir()
	fnames := []string{
		"subfolder1/subfolder11/file1",
		"subfolder2/file2",
		"file3",
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subfolder1", "subfolder11"), 0777))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subfolder2"), 0777))
	for _, fname := range fnames {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fname), []byte(fname), 0777))
	}
	sort.Strings(fnames)
	return dir, fnames
}

func TestHasReadWritePermissions(t *testing.T) {
	type args struct {
		itemPath string
		perms    os.FileMode
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "0600 permissions returns true",
			args: args{
				itemPath: "somefile",
				perms:    file.DefaultIoConfig().ReadWritePermissions,
			},
			want: true,
		},
		{
			name: "other permissions returns false",
			args: args{
				itemPath: "somefile2",
				perms:    file.DefaultIoConfig().ReadWriteExecutePermissions,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullPath := filepath.Join(t.TempDir(), tt.args.itemPath)
			require.NoError(t, os.WriteFile(fullPath, []byte("foo"), tt.args.perms))
			got, err := file.HasReadWritePermissions(fullPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("HasReadWritePermissions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasReadWritePermissions() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteLinesToFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "testfile.txt")
	t.Run("write to a new file", func(t *testing.T) {
		lines := []string{"line1", "line2", "line3"}
		require.NoError(t, file.WriteLinesToFile(lines, filename))
		// Check file content
		content, err := os.ReadFile(filepath.Clean(filename))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		// Join lines with newline for comparison
		expectedContent := strings.Join(lines, "\n") + "\n"
		if string(content) != expectedContent {
			t.Errorf("file content = %q, want %q", string(content), expectedContent)
		}
	})
	t.Run("overwrite existing file", func(t *testing.T) {
		lines := []string{"line4", "line5"}
		require.NoError(t, file.WriteLinesToFile(lines, filename))
		// Check file content
		content, err := os.ReadFile(filepath.Clean(filename))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		// Join lines with newline for comparison
		expectedContent := strings.Join(lines, "\n") + "\n"
		if string(content) != expectedContent {
			t.Errorf("file content = %q, want %q", string(content), expectedContent)
		}
	})
}
