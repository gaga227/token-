package common

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
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
	// http.DetectContentType only recognizes a small set of ISO-BMFF
	// (MP4/MOV) brands and returns application/octet-stream for the most
	// common "isom" brand, so sniff the ftyp box ourselves as a fallback.
	if mimeType == "application/octet-stream" {
		if mime := sniffISOBMFF(head); mime != "" {
			mimeType = mime
		}
	}
	info, ok := assetFileTypes[mimeType]
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrAssetFileTypeUnsupported, mimeType)
	}
	return info.AssetType, info.Extension, nil
}

// mp4Brands and movBrand are the ISO-BMFF major brands considered to be MP4
// video / QuickTime MOV containers. "isom" (the ISO base media file format)
// is produced by nearly every encoder yet is missing from the Go standard
// library's http.DetectContentType brand list.
var (
	mp4Brands = []string{"isom", "iso2", "iso3", "iso4", "iso5", "iso6",
		"mp41", "mp42", "mp4 ", "avc1", "m4v ", "dash", "MSNV", "F4V ",
		"M4A ", "M4B ", "M4P "}
	movBrand = "qt  "
)

// sniffISOBMFF inspects a leading ftyp box and returns a MIME type for
// ISO-BMFF containers, or "" when the header is not an ISO-BMFF file.
func sniffISOBMFF(head []byte) string {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return ""
	}
	boxSize := int(head[3]) | int(head[2])<<8 | int(head[1])<<16 | int(head[0])<<24
	if boxSize < 8 || boxSize%4 != 0 || len(head) < boxSize {
		return ""
	}
	majorBrand := string(head[8:12])
	if majorBrand == movBrand {
		return "video/quicktime"
	}
	for _, brand := range mp4Brands {
		if majorBrand == brand {
			if brand == "M4A " || brand == "M4B " || brand == "M4P " {
				return "audio/mp4"
			}
			return "video/mp4"
		}
	}
	return ""
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

// -----------------------------------------------------------------------------
// 上游回源瘦身
//
// 视频任务提交时，首帧/尾帧/参考图是由上游（方舟、筷子等）**自行回源拉取**
// 的。本网关出口带宽有限，一张 4K 原图动辄 8~15MB，上游按默认超时回源会失败
// 并返回 InternalServiceError（"资源同步失败: url=..."）。
//
// ShrinkAssetURLForUpstream 在转发前把「本网关资产库里体积过大的图片」替换为
// 压缩派生文件的 URL：长边限制 AssetShrinkMaxEdge，JPEG 质量从 85 递减直到
// 体积 ≤ AssetShrinkMaxBytes。派生文件按原 key 哈希命名，重复提交命中缓存，
// 不产生垃圾文件。任何无法处理的输入（外部 URL、非图片、解码失败）都原样
// 返回，调用方不会因此丢请求。
// -----------------------------------------------------------------------------

var (
	// AssetShrinkMaxEdge 是转发给上游的图片长边上限（像素）。
	AssetShrinkMaxEdge = 2048
	// AssetShrinkMaxBytes 是转发给上游的图片体积上限（字节）。
	AssetShrinkMaxBytes int64 = 2 << 20
)

// jpegQualities 是压缩时依次尝试的质量档位。
var shrinkJPEGQualities = []int{85, 75, 65}

// ShrinkAssetURLForUpstream 返回指向图片压缩派生文件的 URL；无需或无法压缩时
// 原样返回 rawURL。
func ShrinkAssetURLForUpstream(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return rawURL
	}
	key, ok := AssetStorageKeyFromURL(rawURL)
	if !ok {
		return rawURL
	}
	fullPath, err := ResolveAssetStoragePath(key)
	if err != nil {
		return rawURL
	}
	info, err := os.Stat(fullPath)
	if err != nil || info.Size() <= AssetShrinkMaxBytes {
		return rawURL
	}

	derivedKey := derivedShrinkAssetKey(key)
	if derivedPath, err := ResolveAssetStoragePath(derivedKey); err == nil {
		if st, statErr := os.Stat(derivedPath); statErr == nil && st.Size() > 0 {
			return replaceAssetKeyInURL(rawURL, derivedKey)
		}
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return rawURL
	}
	src, format, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		return rawURL
	}
	switch format {
	case "jpeg", "png", "webp":
	default:
		return rawURL
	}

	scaled := downscaleToMaxEdge(src, AssetShrinkMaxEdge)
	encoded, err := encodeJPEGUnder(scaled, AssetShrinkMaxBytes)
	if err != nil || len(encoded) == 0 {
		return rawURL
	}
	if err := writeDerivedAssetFile(derivedKey, encoded); err != nil {
		return rawURL
	}
	return replaceAssetKeyInURL(rawURL, derivedKey)
}

// ShrinkAssetURLsForUpstream 批量处理，保持顺序与长度不变。
func ShrinkAssetURLsForUpstream(rawURLs []string) []string {
	if len(rawURLs) == 0 {
		return rawURLs
	}
	out := make([]string, len(rawURLs))
	for i, u := range rawURLs {
		out[i] = ShrinkAssetURLForUpstream(u)
	}
	return out
}

// derivedShrinkAssetKey 由原 key 与压缩参数派生确定性 key，保证幂等。
func derivedShrinkAssetKey(origKey string) string {
	seed := fmt.Sprintf("shrink:%s:edge=%d:q=%d", origKey, AssetShrinkMaxEdge, shrinkJPEGQualities[0])
	sum := sha256.Sum256([]byte(seed))
	digest := hex.EncodeToString(sum[:])
	return digest[0:2] + "/" + digest[2:34] + ".jpg"
}

// replaceAssetKeyInURL 把 URL 中的资产 key 换成 newKey，保留 scheme/host/query。
func replaceAssetKeyInURL(rawURL, newKey string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if idx := strings.Index(parsed.Path, AssetFileURLPrefix); idx >= 0 {
		parsed.Path = parsed.Path[:idx+len(AssetFileURLPrefix)] + newKey
		return parsed.String()
	}
	// OSS 等对象存储：替换最后一段路径。
	if slash := strings.LastIndex(parsed.Path, "/"); slash >= 0 {
		parsed.Path = parsed.Path[:slash+1] + newKey
	}
	return parsed.String()
}

// writeDerivedAssetFile 原子写入派生文件（先写临时文件再 rename）。
func writeDerivedAssetFile(key string, data []byte) error {
	fullPath, err := ResolveAssetStoragePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	tmp := fullPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// downscaleToMaxEdge 按长边等比缩放；未超限时原样返回。
func downscaleToMaxEdge(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	longEdge := w
	if h > longEdge {
		longEdge = h
	}
	if longEdge <= maxEdge || longEdge <= 0 {
		return src
	}
	scale := float64(maxEdge) / float64(longEdge)
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// encodeJPEGUnder 依次降低质量直到编码结果不超过 maxBytes；仍超标时返回最小
// 质量的结果（体积必然远小于原图）。
func encodeJPEGUnder(img image.Image, maxBytes int64) ([]byte, error) {
	var smallest []byte
	for _, quality := range shrinkJPEGQualities {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		encoded := buf.Bytes()
		smallest = encoded
		if int64(len(encoded)) <= maxBytes {
			return encoded, nil
		}
	}
	return smallest, nil
}
