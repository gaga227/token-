package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func faceCallbackContext(t *testing.T, host string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/asset-library/face-verification/callback", nil)
	c.Request.Host = host
	return c
}

// TestFaceVerificationCallbackURL follows the reverse-proxy reality: Terra's
// container only sees plain HTTP from nginx, so the forwarded protocol must win.
func TestFaceVerificationCallbackURL(t *testing.T) {
	const path = "/api/asset-library/face-verification/callback"

	c := faceCallbackContext(t, "token.nexuscore.net.cn")
	c.Request.Header.Set("X-Forwarded-Proto", "https")
	assert.Equal(t, "https://token.nexuscore.net.cn"+path, faceVerificationCallbackURL(c))

	// No forwarded header at all: a public host is assumed to be HTTPS, which is
	// the only shape a real deployment can take.
	assert.Equal(t, "https://token.nexuscore.net.cn"+path, faceVerificationCallbackURL(faceCallbackContext(t, "token.nexuscore.net.cn")))

	// Local development stays on HTTP.
	assert.Equal(t, "http://127.0.0.1:3000"+path, faceVerificationCallbackURL(faceCallbackContext(t, "127.0.0.1:3000")))
	assert.Equal(t, "http://localhost:3000"+path, faceVerificationCallbackURL(faceCallbackContext(t, "localhost:3000")))

	// An explicit proxy header is honoured even when it disagrees with the host.
	c = faceCallbackContext(t, "gateway.internal")
	c.Request.Header.Set("X-Forwarded-Proto", "http")
	assert.Equal(t, "http://gateway.internal"+path, faceVerificationCallbackURL(c))

	// A proxy may append to the header; only the first hop counts.
	c = faceCallbackContext(t, "gateway.example.com")
	c.Request.Header.Set("X-Forwarded-Proto", "https, http")
	assert.Equal(t, "https://gateway.example.com"+path, faceVerificationCallbackURL(c))

	// RFC 7239 form.
	c = faceCallbackContext(t, "gateway.example.com")
	c.Request.Header.Set("Forwarded", `for=10.0.0.1;proto=http;by=10.0.0.2`)
	assert.Equal(t, "http://gateway.example.com"+path, faceVerificationCallbackURL(c))

	// No host means no usable URL; the caller reports the failure instead of
	// registering a broken callback with the upstream.
	assert.Empty(t, faceVerificationCallbackURL(faceCallbackContext(t, "")))
	assert.Empty(t, faceVerificationCallbackURL(nil))
}

func TestFaceVerificationCallbackValuesReadsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/asset-library/face-verification/callback?bytedToken=vvs-20261021120000-c8f2p&resultCode=10000&algorithmBaseRespCode=0&reqMeasureInfoValue=1&verify_type=real_time",
		nil,
	)
	values := faceVerificationCallbackValues(c)
	assert.Equal(t, "vvs-20261021120000-c8f2p", values.Get("bytedToken"))
	assert.Equal(t, "10000", values.Get("resultCode"))
	assert.Equal(t, "real_time", values.Get("verify_type"))

	assert.Empty(t, faceVerificationCallbackValues(nil))
}

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "localhost:3000", "127.0.0.1", "127.0.0.1:3000", "[::1]:8080"} {
		assert.True(t, isLoopbackHost(host), host)
	}
	for _, host := range []string{"token.nexuscore.net.cn", "10.0.0.5:3000", "example.com"} {
		assert.False(t, isLoopbackHost(host), host)
	}
}

// TestFaceVerificationLandingPageRendersVerdict pins the page the end user sees
// after tapping 完成: it must never claim a final verdict, and it must escape a
// resultCode that arrives from an unauthenticated query string.
func TestFaceVerificationLandingPageRendersVerdict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	render := func(code string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		writeFaceVerificationLandingPage(c, service.FaceVerificationCallbackParams{ResultCode: code})
		return recorder
	}

	pass := render("10000")
	assert.Equal(t, http.StatusOK, pass.Code)
	assert.Contains(t, pass.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, pass.Body.String(), "认证信息已提交")
	assert.Contains(t, pass.Body.String(), "正在核对")
	assert.NotContains(t, pass.Body.String(), "resultCode=")

	fail := render("10001")
	assert.Contains(t, fail.Body.String(), "认证未通过")
	assert.Contains(t, fail.Body.String(), "resultCode=10001")

	// resultCode is attacker-controlled: it must not reach the DOM raw.
	hostile := render(`"><script>alert(1)</script>`)
	assert.NotContains(t, hostile.Body.String(), "<script>alert(1)</script>")
	assert.Contains(t, hostile.Body.String(), "&lt;script&gt;")
}
