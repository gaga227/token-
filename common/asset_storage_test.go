package common

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngHead is the magic number of a PNG file followed by filler bytes.
var pngHead = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte("png"), 64)...)

func withTempAssetStorageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := AssetStorageDir
	AssetStorageDir = dir
	t.Cleanup(func() { AssetStorageDir = previous })
	return dir
}

func TestSaveAssetStorageFileStoresPng(t *testing.T) {
	withTempAssetStorageDir(t)
	key, assetType, size, err := SaveAssetStorageFile(bytes.NewReader(pngHead))
	require.NoError(t, err)
	assert.Equal(t, "Image", assetType)
	assert.Equal(t, int64(len(pngHead)), size)
	assert.Regexp(t, `^[0-9a-f]{2}/[0-9a-f]{32}\.png$`, key)

	fullPath, err := ResolveAssetStoragePath(key)
	require.NoError(t, err)
	saved, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, pngHead, saved)

	require.NoError(t, DeleteAssetStorageFile(key))
	_, err = os.Stat(fullPath)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestSaveAssetStorageFileRejectsUnsupportedType(t *testing.T) {
	withTempAssetStorageDir(t)
	_, _, _, err := SaveAssetStorageFile(strings.NewReader("just some plain text that is not an asset"))
	assert.ErrorIs(t, err, ErrAssetFileTypeUnsupported)
}

func TestSaveAssetStorageFileRejectsOversizedFile(t *testing.T) {
	withTempAssetStorageDir(t)
	previous := AssetUploadMaxMB
	AssetUploadMaxMB = 1
	t.Cleanup(func() { AssetUploadMaxMB = previous })
	// 2MB of PNG data exceeds the 1MB limit.
	payload := append(pngHead, bytes.Repeat([]byte{0}, 2<<20)...)
	_, _, _, err := SaveAssetStorageFile(bytes.NewReader(payload))
	assert.ErrorIs(t, err, ErrAssetFileTooLarge)

	// The partially written file must have been cleaned up.
	err = filepath.Walk(AssetStorageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Errorf("leftover file after oversized upload: %s", path)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestResolveAssetStoragePathRejectsTraversal(t *testing.T) {
	withTempAssetStorageDir(t)
	for _, key := range []string{
		"../../etc/passwd",
		"ab/../../../../etc/passwd.png",
		"ab/not-a-uuid.png",
		"AB/0123456789abcdef0123456789abcdef.png",
		"/ab/0123456789abcdef0123456789abcdef.png",
		"",
	} {
		_, err := ResolveAssetStoragePath(key)
		assert.Error(t, err, "key %q should be rejected", key)
	}
}

func TestAssetStorageKeyFromURL(t *testing.T) {
	key, ok := AssetStorageKeyFromURL("https://gateway.example.com/api/asset/files/ab/0123456789abcdef0123456789abcdef.png")
	assert.True(t, ok)
	assert.Equal(t, "ab/0123456789abcdef0123456789abcdef.png", key)

	_, ok = AssetStorageKeyFromURL("https://cdn.example.com/images/photo.png")
	assert.False(t, ok)

	_, ok = AssetStorageKeyFromURL("https://gateway.example.com/api/asset/files/../../etc/passwd")
	assert.False(t, ok)
}
