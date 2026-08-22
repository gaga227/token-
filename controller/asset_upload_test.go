package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalPng is a tiny valid PNG payload used for upload tests.
var minimalPng = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
	bytes.Repeat([]byte("PNG"), 64)...)

func TestUploadAndServeAssetFile(t *testing.T) {
	previousDir := common.AssetStorageDir
	previousAddr := system_setting.ServerAddress
	common.AssetStorageDir = t.TempDir()
	system_setting.ServerAddress = "" // force request-host based URL
	t.Cleanup(func() {
		common.AssetStorageDir = previousDir
		system_setting.ServerAddress = previousAddr
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	r.POST("/api/asset/upload", UploadAssetLibraryFile)
	r.GET("/api/asset/files/*path", ServeAssetLibraryFile)
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	formFile, err := writer.CreateFormFile("file", "test.png")
	require.NoError(t, err)
	_, err = formFile.Write(minimalPng)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	resp, err := http.Post(ts.URL+"/api/asset/upload", writer.FormDataContentType(), bytes.NewReader(body.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var envelope struct {
		Success bool `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Url       string `json:"Url"`
			AssetType string `json:"AssetType"`
			Size      int64  `json:"Size"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	resp.Body.Close()
	require.True(t, envelope.Success, "message: %s", envelope.Message)
	assert.Equal(t, "Image", envelope.Data.AssetType)
	assert.Equal(t, int64(len(minimalPng)), envelope.Data.Size)
	assert.NotEmpty(t, envelope.Data.Url)

	fileResp, err := http.Get(envelope.Data.Url)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, fileResp.StatusCode)
	fileBytes, err := io.ReadAll(fileResp.Body)
	require.NoError(t, err)
	fileResp.Body.Close()
	assert.Equal(t, minimalPng, fileBytes)
	assert.Equal(t, "public, max-age=31536000, immutable", fileResp.Header.Get("Cache-Control"))
}

func TestUploadAssetFileRejectsUnsupportedType(t *testing.T) {
	previousDir := common.AssetStorageDir
	previousAddr := system_setting.ServerAddress
	common.AssetStorageDir = t.TempDir()
	system_setting.ServerAddress = ""
	t.Cleanup(func() {
		common.AssetStorageDir = previousDir
		system_setting.ServerAddress = previousAddr
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	r.POST("/api/asset/upload", UploadAssetLibraryFile)
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	formFile, err := writer.CreateFormFile("file", "notes.txt")
	require.NoError(t, err)
	_, _ = formFile.Write([]byte("just some plain text that is not an asset"))
	require.NoError(t, writer.Close())

	resp, err := http.Post(ts.URL+"/api/asset/upload", writer.FormDataContentType(), bytes.NewReader(body.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServeAssetFileRequiresAuth(t *testing.T) {
	previousDir := common.AssetStorageDir
	common.AssetStorageDir = t.TempDir()
	t.Cleanup(func() { common.AssetStorageDir = previousDir })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/asset/files/*path", ServeAssetLibraryFile)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// traversal attempt must be rejected with 404
	resp, err := http.Get(ts.URL + "/api/asset/files/../../etc/passwd")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
