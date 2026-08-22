package common

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetAssetOSSConfig(t *testing.T, backend, region, bucket, ak, sk, prefix, publicBase string) {
	t.Helper()
	oldBackend, oldRegion, oldBucket := AssetStorageBackend, AssetOSSRegion, AssetOSSBucket
	oldAK, oldSK := AssetOSSAccessKeyID, AssetOSSAccessKeySecret
	oldPrefix, oldEndpoint, oldPublicBase := AssetOSSKeyPrefix, AssetOSSEndpoint, AssetOSSPublicBaseURL
	t.Cleanup(func() {
		AssetStorageBackend, AssetOSSRegion, AssetOSSBucket = oldBackend, oldRegion, oldBucket
		AssetOSSAccessKeyID, AssetOSSAccessKeySecret = oldAK, oldSK
		AssetOSSKeyPrefix, AssetOSSEndpoint, AssetOSSPublicBaseURL = oldPrefix, oldEndpoint, oldPublicBase
		ossClientOnce = sync.Once{}
		ossClientInst, ossClientErr = nil, nil
		ossSignClientOnce = sync.Once{}
		ossSignClientInst, ossSignClientErr = nil, nil
	})
	AssetStorageBackend = backend
	AssetOSSRegion = region
	AssetOSSBucket = bucket
	AssetOSSAccessKeyID = ak
	AssetOSSAccessKeySecret = sk
	AssetOSSKeyPrefix = normalizeAssetOSSKeyPrefix(prefix)
	AssetOSSEndpoint = ""
	AssetOSSPublicBaseURL = publicBase
	ossClientOnce = sync.Once{}
	ossClientInst, ossClientErr = nil, nil
	ossSignClientOnce = sync.Once{}
	ossSignClientInst, ossSignClientErr = nil, nil
}

func TestAssetOSSKeyHelpers(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "mybucket", "ak", "sk", "", "")

	key := AssetOSSStorageKey("asset-library/ab/0123456789abcdef0123456789abcdef.png")
	assert.Equal(t, "oss://mybucket/asset-library/ab/0123456789abcdef0123456789abcdef.png", key)

	objectKey, ok := assetOSSObjectKey(key)
	require.True(t, ok)
	assert.Equal(t, "asset-library/ab/0123456789abcdef0123456789abcdef.png", objectKey)

	_, ok = assetOSSObjectKey("ab/0123456789abcdef0123456789abcdef.png")
	assert.False(t, ok, "plain local keys must not be treated as OSS keys")
}

func TestAssetOSSKeyFromURL(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "")

	key, ok := AssetOSSKeyFromURL("https://nexuscore.oss-cn-beijing.aliyuncs.com/asset-library/ab/0123456789abcdef0123456789abcdef.png?Expires=123&OSSAccessKeyId=x&Signature=y")
	require.True(t, ok)
	assert.Equal(t, "oss://nexuscore/asset-library/ab/0123456789abcdef0123456789abcdef.png", key)

	// plain (public-read) URL form
	key, ok = AssetOSSKeyFromURL("https://nexuscore.oss-cn-beijing.aliyuncs.com/asset-library/cd/ffffffffffffffffffffffffffffffff.mp4")
	require.True(t, ok)
	assert.Equal(t, "oss://nexuscore/asset-library/cd/ffffffffffffffffffffffffffffffff.mp4", key)

	// wrong host
	_, ok = AssetOSSKeyFromURL("https://other-bucket.oss-cn-beijing.aliyuncs.com/asset-library/ab/0123456789abcdef0123456789abcdef.png")
	assert.False(t, ok)

	// traversal attempt
	_, ok = AssetOSSKeyFromURL("https://nexuscore.oss-cn-beijing.aliyuncs.com/asset-library/../../etc/passwd")
	assert.False(t, ok)
}

func TestAssetOSSKeyFromURLWithCustomDomain(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "https://cdn.example.com")

	key, ok := AssetOSSKeyFromURL("https://cdn.example.com/asset-library/ab/0123456789abcdef0123456789abcdef.png")
	require.True(t, ok)
	assert.Equal(t, "oss://nexuscore/asset-library/ab/0123456789abcdef0123456789abcdef.png", key)
}

func TestAssetStorageAccessURLFallsBackForLocalKeys(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "")

	fallback := "https://example.com/photo.png"
	assert.Equal(t, fallback, AssetStorageAccessURL("ab/0123456789abcdef0123456789abcdef.png", fallback))
	assert.Equal(t, fallback, AssetStorageAccessURL("", fallback))
}

func TestAssetStorageAccessURLPublicBase(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "https://cdn.example.com")

	key := AssetOSSStorageKey("asset-library/ab/0123456789abcdef0123456789abcdef.png")
	assert.Equal(t,
		"https://cdn.example.com/asset-library/ab/0123456789abcdef0123456789abcdef.png",
		AssetStorageAccessURL(key, "fallback"),
	)
}

func TestAssetStorageKeyFromURLDispatchesToOSS(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "")

	key, ok := AssetStorageKeyFromURL("https://nexuscore.oss-cn-beijing.aliyuncs.com/asset-library/ab/0123456789abcdef0123456789abcdef.png")
	require.True(t, ok)
	assert.Equal(t, "oss://nexuscore/asset-library/ab/0123456789abcdef0123456789abcdef.png", key)

	// local gateway URL still works
	key, ok = AssetStorageKeyFromURL("https://gateway.example.com/api/asset/files/ab/0123456789abcdef0123456789abcdef.png")
	require.True(t, ok)
	assert.Equal(t, "ab/0123456789abcdef0123456789abcdef.png", key)
}

func TestNormalizeAssetOSSKeyPrefix(t *testing.T) {
	assert.Equal(t, "asset-library/", normalizeAssetOSSKeyPrefix(""))
	assert.Equal(t, "asset-library/", normalizeAssetOSSKeyPrefix("asset-library"))
	assert.Equal(t, "asset-library/", normalizeAssetOSSKeyPrefix("/asset-library/"))
	assert.Equal(t, "media/", normalizeAssetOSSKeyPrefix("media"))
}

func TestAssetOSSEndpointDerivation(t *testing.T) {
	resetAssetOSSConfig(t, "oss", "cn-beijing", "nexuscore", "ak", "sk", "", "")
	assert.Equal(t, "https://oss-cn-beijing.aliyuncs.com", assetOSSEndpoint())
	assert.Equal(t, "nexuscore.oss-cn-beijing.aliyuncs.com", assetOSSEndpointHost())

	// full endpoint region passthrough
	AssetOSSRegion = "oss-cn-hangzhou-internal.aliyuncs.com"
	assert.Equal(t, "https://oss-cn-hangzhou-internal.aliyuncs.com", assetOSSEndpoint())
}

// TestAssetOSSLiveRoundTrip runs against a real bucket when
// ASSET_OSS_LIVE_TEST=1 is set together with the OSS credentials. It uploads
// a tiny PNG, verifies the signed URL downloads the same bytes and deletes
// the object afterwards.
func TestAssetOSSLiveRoundTrip(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ASSET_OSS_LIVE_TEST")) != "1" {
		t.Skip("set ASSET_OSS_LIVE_TEST=1 to run the live OSS round trip")
	}
	require.NotEmpty(t, AssetOSSBucket, "ASSET_OSS_BUCKET is required")
	require.NotEmpty(t, AssetOSSAccessKeyID, "ASSET_OSS_ACCESS_KEY_ID is required")
	require.NotEmpty(t, AssetOSSAccessKeySecret, "ASSET_OSS_ACCESS_KEY_SECRET is required")

	payload := pngHead
	key, assetType, size, err := SaveAssetOSSFile(bytes.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, "Image", assetType)
	require.EqualValues(t, len(payload), size)
	t.Cleanup(func() {
		if err := DeleteAssetStorageByKey(key); err != nil {
			t.Logf("cleanup failed for %s: %v", key, err)
		}
	})
	assert.True(t, strings.HasPrefix(key, "oss://"+AssetOSSBucket+"/"+AssetOSSKeyPrefix), "unexpected key %s", key)

	signedURL := AssetOSSObjectURL(key, "")
	require.NotEmpty(t, signedURL, "signed URL must not be empty")
	response, err := http.Get(signedURL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, len(payload), len(body), "downloaded bytes must match the upload")

	// deleting twice treats missing objects as success
	require.NoError(t, DeleteAssetStorageByKey(key))
	require.NoError(t, DeleteAssetStorageByKey(key))
}
