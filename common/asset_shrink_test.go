package common

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// newTempAssetDir 把资产库指向临时目录，测试结束恢复。
func newTempAssetDir(t *testing.T) {
	t.Helper()
	orig := AssetStorageDir
	AssetStorageDir = t.TempDir()
	t.Cleanup(func() { AssetStorageDir = orig })
}

// saveNoiseJPEG 生成 w×h 的随机噪声 JPEG 并落库，返回 (url, key, size)。
// 噪声保证压缩率极低，从而稳定超过大小阈值。
func saveNoiseJPEG(t *testing.T, w, h int) (url string, key string, size int64) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := rand.New(rand.NewSource(42))
	raw := make([]byte, w*h*4)
	if _, err := rnd.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	for i := 3; i < len(raw); i += 4 {
		raw[i] = 255
	}
	copy(img.Pix, raw)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	savedKey, _, savedSize, err := SaveAssetStorageFile(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("SaveAssetStorageFile: %v", err)
	}
	return "https://gw.example.com" + AssetFileURLPrefix + savedKey, savedKey, savedSize
}

// 超大图片（体积与长边双超限）应被替换为压缩派生 URL，且派生文件真实可读、
// 长边与体积都落在阈值内。
func TestShrinkAssetURLForUpstreamCompressesLargeImage(t *testing.T) {
	newTempAssetDir(t)
	url, origKey, size := saveNoiseJPEG(t, 2600, 1700)
	if size <= AssetShrinkMaxBytes {
		t.Fatalf("test fixture too small: %d bytes", size)
	}

	shrunk := ShrinkAssetURLForUpstream(url)
	if shrunk == url {
		t.Fatalf("large asset URL must be replaced, got unchanged %s", url)
	}
	if !strings.HasPrefix(shrunk, "https://gw.example.com"+AssetFileURLPrefix) {
		t.Fatalf("derived URL must keep scheme/host/prefix, got %s", shrunk)
	}

	derivedKey, ok := AssetStorageKeyFromURL(shrunk)
	if !ok {
		t.Fatalf("derived URL key not recognized: %s", shrunk)
	}
	if derivedKey == origKey {
		t.Fatalf("derived key must differ from original")
	}

	path, err := ResolveAssetStoragePath(derivedKey)
	if err != nil {
		t.Fatalf("resolve derived: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat derived: %v", err)
	}
	if info.Size() > AssetShrinkMaxBytes {
		t.Fatalf("derived size %d exceeds limit %d", info.Size(), AssetShrinkMaxBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open derived: %v", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode derived: %v", err)
	}
	if cfg.Width > AssetShrinkMaxEdge || cfg.Height > AssetShrinkMaxEdge {
		t.Fatalf("derived edge %dx%d exceeds %d", cfg.Width, cfg.Height, AssetShrinkMaxEdge)
	}
	// 长宽比应保持
	if cfg.Width != AssetShrinkMaxEdge {
		t.Fatalf("expected long edge %d, got %d", AssetShrinkMaxEdge, cfg.Width)
	}
}

// 派生结果是幂等的：重复调用返回同一 URL，且不重复写文件。
func TestShrinkAssetURLForUpstreamIsIdempotent(t *testing.T) {
	newTempAssetDir(t)
	url, _, _ := saveNoiseJPEG(t, 2600, 1700)

	first := ShrinkAssetURLForUpstream(url)
	second := ShrinkAssetURLForUpstream(url)
	if first != second {
		t.Fatalf("expected idempotent result, got %s vs %s", first, second)
	}
}

// 小图与外部 URL 必须原样返回。
func TestShrinkAssetURLForUpstreamLeavesSmallAndForeignURLs(t *testing.T) {
	newTempAssetDir(t)
	smallURL, _, _ := saveNoiseJPEG(t, 320, 240)
	if got := ShrinkAssetURLForUpstream(smallURL); got != smallURL {
		t.Fatalf("small asset must be untouched, got %s", got)
	}
	foreign := "https://cdn.other.com/photo.jpg"
	if got := ShrinkAssetURLForUpstream(foreign); got != foreign {
		t.Fatalf("foreign URL must be untouched, got %s", got)
	}
	if got := ShrinkAssetURLForUpstream(""); got != "" {
		t.Fatalf("empty URL must stay empty")
	}
}

func TestShrinkAssetURLsForUpstreamKeepsShape(t *testing.T) {
	newTempAssetDir(t)
	bigURL, _, _ := saveNoiseJPEG(t, 2600, 1700)
	input := []string{bigURL, "https://cdn.other.com/a.jpg", ""}
	out := ShrinkAssetURLsForUpstream(input)
	if len(out) != len(input) {
		t.Fatalf("length must be preserved")
	}
	if out[0] == bigURL {
		t.Fatalf("first element should be shrunk")
	}
	if out[1] != input[1] || out[2] != input[2] {
		t.Fatalf("foreign/empty entries must be untouched")
	}
}
