package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseFaceVerificationCallbackMapsUpstreamNames pins the field mapping: the
// upstream talks in bytedToken/resultCode, we speak verification id/status.
func TestParseFaceVerificationCallbackMapsUpstreamNames(t *testing.T) {
	values := url.Values{
		"bytedToken":            {"vvs-20261021120000-c8f2p"},
		"resultCode":            {"10000"},
		"algorithmBaseRespCode": {"0"},
		"reqMeasureInfoValue":   {"1"},
		"verify_type":           {"real_time"},
	}
	params := ParseFaceVerificationCallback(values)
	assert.Equal(t, "vvs-20261021120000-c8f2p", params.VerificationId)
	assert.Equal(t, "10000", params.ResultCode)
	assert.Equal(t, "0", params.AlgorithmBaseRespCode)
	assert.Equal(t, "1", params.MeasureInfoValue)
	assert.Equal(t, "real_time", params.VerifyType)

	// A different casing from another upstream build must not lose the handle.
	alternative := ParseFaceVerificationCallback(url.Values{
		"BytedToken":  {"vvs-1"},
		"result_code": {"1"},
	})
	assert.Equal(t, "vvs-1", alternative.VerificationId)
	assert.Equal(t, "1", alternative.ResultCode)

	assert.Empty(t, ParseFaceVerificationCallback(url.Values{}).VerificationId)
}

// TestParseFaceVerificationCallbackJSONRecoversToken covers the JSON safety net:
// the documented transport is a query string, but a body must not silently drop
// the result.
func TestParseFaceVerificationCallbackJSONRecoversToken(t *testing.T) {
	payload := []byte(`{"Result":{"bytedToken":"vvs-20261021120000-abcde","resultCode":"10000"}}`)
	parsed := ParseFaceVerificationCallbackJSON(payload)
	assert.Equal(t, "vvs-20261021120000-abcde", parsed["bytedToken"])
	assert.Equal(t, "10000", parsed["resultCode"])

	// Not JSON at all: the token prefix alone is enough to identify the session.
	raw := ParseFaceVerificationCallbackJSON([]byte(`ok vvs-20261021120000-zzzz; done`))
	assert.Equal(t, "vvs-20261021120000-zzzz", raw["bytedToken"])

	assert.Empty(t, ParseFaceVerificationCallbackJSON(nil))
}

// TestApplyFaceVerificationCallbackRequiresToken keeps an empty notification from
// being treated as a session refresh.
func TestApplyFaceVerificationCallbackRequiresToken(t *testing.T) {
	setupAssetLibraryServiceTestDB(t)
	err := ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		ResultCode: faceVerificationSuccessResultCode,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bytedToken")
}

// TestApplyFaceVerificationCallbackIgnoresUnknownToken covers the forged-request
// path: an unknown handle must be a no-op, not an error and not a side effect.
func TestApplyFaceVerificationCallbackIgnoresUnknownToken(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId: "vvs-does-not-exist",
		ResultCode:     faceVerificationSuccessResultCode,
	}))

	var count int64
	require.NoError(t, db.Model(&model.UserFaceVerification{}).Count(&count).Error)
	assert.Zero(t, count)
}

func faceVerificationCallbackFixture(t *testing.T, upstream http.HandlerFunc) (*model.UserFaceVerification, *int) {
	t.Helper()
	db := setupAssetLibraryServiceTestDB(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		upstream(writer, request)
	}))
	t.Cleanup(server.Close)

	config := shujubaoTestConfig(server.URL)
	encrypted, err := common.EncryptText(config.APIKey)
	require.NoError(t, err)
	config.APIKey = encrypted
	require.NoError(t, db.Create(config).Error)

	verification := &model.UserFaceVerification{
		UserId:         1,
		ChannelId:      config.ChannelId,
		VerificationId: "vvs-20261021120000-c8f2p",
		Status:         model.FaceVerificationStatusWaitingUser,
		ExpiresAt:      time.Now().Add(time.Minute).Unix(),
	}
	require.NoError(t, model.CreateUserFaceVerification(verification))

	return verification, &calls
}

// TestApplyFaceVerificationCallbackRejectionSkipsUpstream proves a rejection is
// taken from resultCode without spending an upstream round-trip.
func TestApplyFaceVerificationCallbackRejectionSkipsUpstream(t *testing.T) {
	verification, calls := faceVerificationCallbackFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Result":{"GroupId":"group-1"}}`))
	})

	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId:        verification.VerificationId,
		ResultCode:            "10001",
		AlgorithmBaseRespCode: "5001",
	}))

	stored, err := model.GetUserFaceVerification(verification.UserId, verification.VerificationId)
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusFailed, stored.Status)
	assert.Contains(t, stored.LastError, "resultCode=10001")
	assert.Contains(t, stored.LastError, "algorithmBaseRespCode=5001")
	assert.Zero(t, *calls, "a rejection must not trigger an upstream lookup")
}

// TestApplyFaceVerificationCallbackConfirmsWithUpstream is the happy path: the
// notification says success, the upstream confirms it, the group is claimed.
func TestApplyFaceVerificationCallbackConfirmsWithUpstream(t *testing.T) {
	verification, calls := faceVerificationCallbackFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "GetVisualValidateResult", request.URL.Query().Get("Action"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-cb"},"Result":{"GroupId":"group-20261021120000-b5e9m"}}`))
	})

	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId: verification.VerificationId,
		ResultCode:     faceVerificationSuccessResultCode,
	}))

	stored, err := model.GetUserFaceVerification(verification.UserId, verification.VerificationId)
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusVerified, stored.Status)
	assert.Equal(t, "group-20261021120000-b5e9m", stored.UpstreamGroupId)
	assert.NotEmpty(t, stored.GroupId, "the upstream group must be claimed locally")
	assert.Empty(t, stored.LastError)
	assert.Equal(t, 1, *calls)

	group, err := model.GetUserAssetGroup(verification.UserId, stored.GroupId)
	require.NoError(t, err)
	assert.Equal(t, model.UserAssetGroupTypeLivenessFace, group.GroupType)
	assert.Equal(t, model.UserAssetGroupOriginLiveness, group.Origin)
}

// TestApplyFaceVerificationCallbackDoesNotTrustForgedSuccess is the security
// case: a public endpoint means anyone can claim success, so the state may only
// advance if the upstream really published a group.
func TestApplyFaceVerificationCallbackDoesNotTrustForgedSuccess(t *testing.T) {
	verification, _ := faceVerificationCallbackFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-cb2"},"Result":{}}`))
	})

	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId: verification.VerificationId,
		ResultCode:     faceVerificationSuccessResultCode,
	}))

	stored, err := model.GetUserFaceVerification(verification.UserId, verification.VerificationId)
	require.NoError(t, err)
	assert.NotEqual(t, model.FaceVerificationStatusVerified, stored.Status)
	assert.Equal(t, model.FaceVerificationStatusCallbackReceived, stored.Status)
	assert.Empty(t, stored.GroupId)
}

// TestApplyFaceVerificationCallbackBypassesLocalExpiry pins the interaction with
// the 15 minute deadline: a notification arriving seconds after the window
// closed still describes a verification the person really completed, so it must
// not be discarded as expired.
func TestApplyFaceVerificationCallbackBypassesLocalExpiry(t *testing.T) {
	verification, _ := faceVerificationCallbackFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Result":{"GroupId":"group-20261021120000-late1"}}`))
	})
	verification.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	require.NoError(t, model.UpdateUserFaceVerification(verification))

	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId: verification.VerificationId,
		ResultCode:     faceVerificationSuccessResultCode,
	}))

	stored, err := model.GetUserFaceVerification(verification.UserId, verification.VerificationId)
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusVerified, stored.Status)

	// Polling, by contrast, still treats the deadline as terminal.
	assert.True(t, faceVerificationExpired(&model.UserFaceVerification{
		Status:    model.FaceVerificationStatusWaitingUser,
		ExpiresAt: verification.ExpiresAt,
	}))
}

// TestApplyFaceVerificationCallbackIgnoresTerminalSession keeps a late or
// duplicated notification from reopening a settled session.
func TestApplyFaceVerificationCallbackIgnoresTerminalSession(t *testing.T) {
	verification, calls := faceVerificationCallbackFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Result":{"GroupId":"group-should-not-be-used"}}`))
	})
	verification.Status = model.FaceVerificationStatusExpired
	require.NoError(t, model.UpdateUserFaceVerification(verification))

	require.NoError(t, ApplyFaceVerificationCallback(context.Background(), FaceVerificationCallbackParams{
		VerificationId: verification.VerificationId,
		ResultCode:     faceVerificationSuccessResultCode,
	}))

	stored, err := model.GetUserFaceVerification(verification.UserId, verification.VerificationId)
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusExpired, stored.Status)
	assert.Empty(t, stored.GroupId)
	assert.Zero(t, *calls)
}
