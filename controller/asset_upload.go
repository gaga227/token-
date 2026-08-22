package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

type assetUploadResult struct {
	URL       string `json:"Url"`
	AssetType string `json:"AssetType"`
	Size      int64  `json:"Size"`
}

// UploadAssetLibraryFile handles multipart uploads of local asset files.
// The returned URL can be used as the SourceURL when creating an asset.
func UploadAssetLibraryFile(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "user identity is missing"})
		return
	}
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "file field is required: " + err.Error()})
		return
	}
	defer file.Close()

	var key, assetType string
	var size int64
	if common.AssetStorageUseOSS() {
		key, assetType, size, err = common.SaveAssetOSSFile(file)
	} else {
		key, assetType, size, err = common.SaveAssetStorageFile(file)
	}
	if err != nil {
		switch {
		case errors.Is(err, common.ErrAssetFileTypeUnsupported):
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		case errors.Is(err, common.ErrAssetVideoFPSInvalid),
			errors.Is(err, common.ErrAssetVideoDurationInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		case errors.Is(err, common.ErrAssetFileTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to store asset file: " + err.Error()})
		}
		return
	}
	fileURL := ""
	if common.AssetStorageUseOSS() {
		fileURL = common.AssetOSSObjectURL(key, "")
	} else {
		fileURL = buildAssetLibraryFileURL(c, key)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": assetUploadResult{
			URL:       fileURL,
			AssetType: assetType,
			Size:      size,
		},
	})
}

// ServeAssetLibraryFile serves locally stored asset files. The route is
// intentionally public so that upstream asset library channels can fetch the
// file when replicating an asset.
func ServeAssetLibraryFile(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("path"), "/")
	fullPath, err := common.ResolveAssetStoragePath(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "asset file not found"})
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.File(fullPath)
}

// buildAssetLibraryFileURL returns the externally reachable URL for a stored
// asset file. It prefers the configured ServerAddress and falls back to the
// request host when the setting is unset or points at localhost.
func buildAssetLibraryFileURL(c *gin.Context, key string) string {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	if base == "" || strings.Contains(base, "localhost") || strings.Contains(base, "127.0.0.1") {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		if forwardedProto := c.GetHeader("X-Forwarded-Proto"); forwardedProto != "" {
			scheme = forwardedProto
		}
		host := c.Request.Host
		if forwardedHost := c.GetHeader("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}
		base = scheme + "://" + host
	}
	return base + common.AssetFileURLPrefix + key
}
