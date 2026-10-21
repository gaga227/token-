package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// faceVerificationCallbackPath is the public path registered with the upstream
// as CallbackURL. It is appended to whatever host served the request, so the
// same binary works on every deployment without a configuration knob.
const faceVerificationCallbackPath = "/api/asset-library/face-verification/callback"

// faceVerificationCallbackTimeout bounds how long a notification is allowed to
// hold the connection after the acknowledgement has been flushed.
const faceVerificationCallbackTimeout = 15 * time.Second

// AssetLibraryFaceVerificationCallback receives the upstream liveness result.
//
// The route is deliberately unauthenticated: the upstream has no gateway
// credential. Nothing in the payload is trusted — it only identifies which
// session to refresh, and the authoritative state is then read back from
// GetVisualValidateResult. A forged notification therefore costs one harmless
// upstream lookup and can never mark somebody as verified.
func AssetLibraryFaceVerificationCallback(c *gin.Context) {
	params := service.ParseFaceVerificationCallback(faceVerificationCallbackValues(c))
	// Acknowledge before doing any work: the upstream must not retry a
	// notification because our upstream round-trip was slow.
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
	c.Writer.Flush()

	verificationId := strings.TrimSpace(params.VerificationId)
	if verificationId == "" {
		common.SysError("asset library face verification callback carries no bytedToken")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), faceVerificationCallbackTimeout)
	defer cancel()
	if err := service.ApplyFaceVerificationCallback(ctx, params); err != nil {
		common.SysError(fmt.Sprintf(
			"asset library face verification callback %s failed: %v",
			common.MaskSensitiveInfo(verificationId),
			err,
		))
	}
}

// faceVerificationCallbackValues collects the notification parameters. The
// upstream documented shape is a query string, but a form-encoded or JSON body
// is accepted as well so a protocol variant cannot silently drop the result.
func faceVerificationCallbackValues(c *gin.Context) url.Values {
	if c == nil || c.Request == nil {
		return url.Values{}
	}
	values := url.Values{}
	// ParseForm merges the query string with an urlencoded body.
	if err := c.Request.ParseForm(); err == nil {
		for key, list := range c.Request.Form {
			values[key] = append([]string(nil), list...)
		}
	}
	if strings.TrimSpace(values.Get("bytedToken")) != "" {
		return values
	}
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if !strings.Contains(contentType, "json") {
		return values
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
	if err != nil || len(raw) == 0 {
		return values
	}
	for key, value := range service.ParseFaceVerificationCallbackJSON(raw) {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	return values
}

// faceVerificationCallbackURL derives the absolute CallbackURL for the current
// request. Behind a reverse proxy the TLS state lives in the forwarded headers,
// so those win; a plain non-loopback host is assumed to be HTTPS because that is
// the only sane shape for a public deployment.
func faceVerificationCallbackURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		return ""
	}
	scheme := forwardedScheme(c)
	if scheme == "" {
		if c.Request.TLS != nil || !isLoopbackHost(host) {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host + faceVerificationCallbackPath
}

func forwardedScheme(c *gin.Context) string {
	for _, header := range []string{"X-Forwarded-Proto", "X-Forwarded-Protocol", "X-Forwarded-Scheme"} {
		value := strings.ToLower(strings.TrimSpace(strings.Split(c.GetHeader(header), ",")[0]))
		if value == "http" || value == "https" {
			return value
		}
	}
	// RFC 7239: Forwarded: for=...;proto=https;by=...
	for _, entry := range strings.Split(c.GetHeader("Forwarded"), ",") {
		for _, part := range strings.Split(entry, ";") {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 || !strings.EqualFold(pair[0], "proto") {
				continue
			}
			value := strings.ToLower(strings.Trim(pair[1], `"`))
			if value == "http" || value == "https" {
				return value
			}
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if bracket := strings.LastIndex(host, "]"); bracket >= 0 {
		host = host[:bracket+1]
	} else if colon := strings.LastIndex(host, ":"); colon >= 0 {
		host = host[:colon]
	}
	switch host {
	case "localhost", "127.0.0.1", "[::1]", "0.0.0.0":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}
