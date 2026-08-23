package common

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AssetStorageDir is the root directory that stores locally uploaded asset
// files. Configure via the ASSET_STORAGE_DIR environment variable.
var AssetStorageDir = "data/asset-library"

// AssetUploadMaxMB limits the size of a single uploaded asset file.
// Configure via the ASSET_UPLOAD_MAX_MB environment variable.
var AssetUploadMaxMB = 100

// AssetFileURLPrefix is the public URL path prefix under which uploaded asset
// files are served by the gateway.
const AssetFileURLPrefix = "/api/asset/files/"

var (
	ErrAssetFileTypeUnsupported = errors.New("asset file type is not supported")
	ErrAssetFileTooLarge        = errors.New("asset file exceeds the upload size limit")
)

func init() {
	if v := strings.TrimSpace(os.Getenv("ASSET_STORAGE_DIR")); v != "" {
		AssetStorageDir = v
	}
	if v := strings.TrimSpace(os.Getenv("ASSET_UPLOAD_MAX_MB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			AssetUploadMaxMB = n
		}
	}
}

// assetFileTypes maps supported MIME types to (asset type, file extension).
var assetFileTypes = map[string]struct {
	AssetType string
	Extension string
}{
	"image/png":       {AssetType: "Image", Extension: ".png"},
	"image/jpeg":      {AssetType: "Image", Extension: ".jpg"},
	"image/webp":      {AssetType: "Image", Extension: ".webp"},
	"image/gif":       {AssetType: "Image", Extension: ".gif"},
	"video/mp4":       {AssetType: "Video", Extension: ".mp4"},
	"video/webm":      {AssetType: "Video", Extension: ".webm"},
	"video/quicktime": {AssetType: "Video", Extension: ".mov"},
	"audio/mpeg":      {AssetType: "Audio", Extension: ".mp3"},
	"audio/wav":       {AssetType: "Audio", Extension: ".wav"},
	"audio/ogg":       {AssetType: "Audio", Extension: ".ogg"},
	"audio/mp4":       {AssetType: "Audio", Extension: ".m4a"},
}

// assetStorageKeyPattern enforces the "<shard>/<uuid><ext>" layout generated
// by SaveAssetStorageFile so that path traversal is impossible.
var assetStorageKeyPattern = regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{32}\.[a-z0-9]{1,8}$`)

// DetectAssetFileType sniffs the beginning of a file and returns the asset
// library asset type and the canonical file extension.
func DetectAssetFileType(head []byte) (assetType string, extension string, err error) {
	mimeType := http.DetectContentType(head)
	if semicolon := strings.Index(mimeType, ";"); semicolon >= 0 {
		mimeType = strings.TrimSpace(mimeType[:semicolon])
	}
	info, ok := assetFileTypes[mimeType]
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrAssetFileTypeUnsupported, mimeType)
	}
	return info.AssetType, info.Extension, nil
}

// SaveAssetStorageFile reads src, sniffs the file type, enforces the upload
// size limit and persists the file under AssetStorageDir. It returns the
// storage key ("<shard>/<uuid><ext>"), the asset type and the file size.
func SaveAssetStorageFile(src io.Reader) (key string, assetType string, size int64, err error) {
	head := make([]byte, 512)
	headSize, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", "", 0, err
	}
	var extension string
	assetType, extension, err = DetectAssetFileType(head[:headSize])
	if err != nil {
		return "", "", 0, err
	}

	id := GetUUID()
	key = id[:2] + "/" + id + extension
	fullPath := filepath.Join(AssetStorageDir, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", "", 0, err
	}
	file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", "", 0, err
	}
	defer file.Close()

	maxBytes := int64(AssetUploadMaxMB) << 20
	reader := io.MultiReader(bytes.NewReader(head[:headSize]), src)
	size, err = io.Copy(file, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		_ = os.Remove(fullPath)
		return "", "", 0, err
	}
	if size > maxBytes {
		_ = os.Remove(fullPath)
		return "", "", 0, fmt.Errorf("%w: limit is %d MB", ErrAssetFileTooLarge, AssetUploadMaxMB)
	}
	if size == 0 {
		_ = os.Remove(fullPath)
		return "", "", 0, errors.New("asset file is empty")
	}
	if assetType == "Video" {
		if err := validateStoredAssetVideo(fullPath); err != nil {
			_ = os.Remove(fullPath)
			return "", "", 0, err
		}
	}
	return key, assetType, size, nil
}

// ResolveAssetStoragePath maps a storage key to its absolute path on disk and
// rejects malformed keys.
func ResolveAssetStoragePath(key string) (string, error) {
	if !assetStorageKeyPattern.MatchString(key) {
		return "", errors.New("invalid asset storage key")
	}
	fullPath := filepath.Join(AssetStorageDir, filepath.FromSlash(key))
	root, err := filepath.Abs(AssetStorageDir)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	if absolute != root && !strings.HasPrefix(absolute, root+string(os.PathSeparator)) {
		return "", errors.New("asset storage key escapes the storage root")
	}
	return absolute, nil
}

// OpenAssetStorageFile opens a locally stored asset file for reading.
func OpenAssetStorageFile(key string) (io.ReadCloser, error) {
	fullPath, err := ResolveAssetStoragePath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(fullPath)
}

// DeleteAssetStorageFile removes a locally stored asset file. A missing file
// is treated as success.
func DeleteAssetStorageFile(key string) error {
	if key == "" {
		return nil
	}
	fullPath, err := ResolveAssetStoragePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AssetStorageKeyFromURL extracts the storage key from an asset file URL.
// It recognizes both gateway URLs ("https://host/api/asset/files/ab/uuid.png")
// and OSS object URLs (signed or plain). The second return value reports
// whether the URL points at a storage backend owned by this gateway.
func AssetStorageKeyFromURL(rawURL string) (string, bool) {
	if key, ok := AssetOSSKeyFromURL(rawURL); ok {
		return key, true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	path := parsed.Path
	if !strings.HasPrefix(path, AssetFileURLPrefix) {
		return "", false
	}
	key := strings.TrimPrefix(path, AssetFileURLPrefix)
	if !assetStorageKeyPattern.MatchString(key) {
		return "", false
	}
	return key, true
}
