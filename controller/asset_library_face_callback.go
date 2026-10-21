package controller

import (
	"context"
	"fmt"
	"html"
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

// faceVerificationSuccessCode mirrors the protocol value documented upstream:
// resultCode 10000 means the liveness check passed.
const faceVerificationSuccessCode = "10000"

// faceVerificationCallbackTimeout bounds how long a notification is allowed to
// hold the connection after the acknowledgement has been flushed.
const faceVerificationCallbackTimeout = 15 * time.Second

// AssetLibraryFaceVerificationCallback is the page the end user lands on after
// tapping "完成" in the liveness page, carrying bytedToken/resultCode in the
// query string. It is also the fallback CallbackURL when the caller supplied no
// ReturnUrl of their own.
//
// The route is deliberately unauthenticated: the browser has no gateway
// credential. Nothing in the payload is trusted — it only identifies which
// session to refresh, and the authoritative state is then read back from
// GetVisualValidateResult. A forged visit therefore costs one harmless upstream
// lookup and can never mark somebody as verified.
func AssetLibraryFaceVerificationCallback(c *gin.Context) {
	params := service.ParseFaceVerificationCallback(faceVerificationCallbackValues(c))
	verificationId := strings.TrimSpace(params.VerificationId)
	if verificationId == "" {
		// No browser redirect involved: keep answering machine-readable JSON so
		// health probes and integrations are unaffected.
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
		common.SysError("asset library face verification callback carries no bytedToken")
		return
	}
	// Answer before doing any work: the browser is waiting on this response and
	// the verification itself is resolved asynchronously below.
	writeFaceVerificationLandingPage(c, params)
	c.Writer.Flush()

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

// writeFaceVerificationLandingPage renders the human-readable page for the end
// user. It never claims a final verdict: the authoritative result is resolved
// asynchronously against the upstream, so the page only repeats what the
// redirect itself carried.
func writeFaceVerificationLandingPage(c *gin.Context, params service.FaceVerificationCallbackParams) {
	code := strings.TrimSpace(params.ResultCode)
	title := "认证信息已提交"
	detail := "认证结果正在核对，请返回应用查看最终状态。本页可以关闭。"
	hint := ""
	if code != "" && code != faceVerificationSuccessCode {
		title = "认证未通过"
		detail = "本次认证未通过，请返回应用重新发起认证。"
		hint = "resultCode=" + code
	}
	page := fmt.Sprintf(faceVerificationLandingHTML, html.EscapeString(title), html.EscapeString(detail), html.EscapeString(hint))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

// faceVerificationLandingHTML is a self-contained page: the end user is on a
// mobile browser with no guarantee of network access back to us for assets.
const faceVerificationLandingHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>真人认证</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#f7f8fa;color:#1a2233;
font-family:-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}
.card{background:#fff;border-radius:14px;box-shadow:0 8px 30px rgba(16,24,40,.08);
padding:38px 30px;max-width:min(92vw,420px);text-align:center}
h1{font-size:20px;margin:0 0 14px;font-weight:600}
p{font-size:15px;line-height:1.7;color:#4a5568;margin:0}
small{display:block;margin-top:18px;color:#8b94a7;font-size:13px;word-break:break-all}
</style>
</head>
<body>
<main class="card">
<h1>%s</h1>
<p>%s</p>
<small>%s</small>
</main>
</body>
</html>
`

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
