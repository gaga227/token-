package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callAssetLibraryAction(t *testing.T, action string, userId int, body string) (*httptest.ResponseRecorder, assetLibraryResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost,
		"/api/asset-library?Action="+action+"&Version=2024-01-01",
		bytes.NewBufferString(body),
	)
	context.Set("id", userId)
	context.Set("group", "default")

	AssetLibraryAction(context)

	var response assetLibraryResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestCreateFaceVerificationReportsUnsupportedWhenNoChannelQualifies(t *testing.T) {
	setupAssetLibraryControllerTestDB(t)
	recorder, response := callAssetLibraryAction(t, "CreateFaceVerification", 1, `{}`)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotNil(t, response.ResponseMetadata.Error)
	assert.Equal(t, "AssetLibraryUnavailable", response.ResponseMetadata.Error.Code)
	assert.Equal(t, "CreateFaceVerification", response.ResponseMetadata.Action)
}

func TestCreateFaceVerificationRejectsInvalidReturnUrl(t *testing.T) {
	setupAssetLibraryControllerTestDB(t)
	for _, body := range []string{
		`{"ReturnUrl":"ftp://customer.example.com/back"}`,
		`{"ReturnUrl":"https://user:pass@customer.example.com/back"}`,
		`{"ReturnUrl":"not-a-url"}`,
	} {
		recorder, response := callAssetLibraryAction(t, "CreateFaceVerification", 1, body)
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
		require.NotNil(t, response.ResponseMetadata.Error)
		assert.Equal(t, "InvalidParameter.ReturnUrl", response.ResponseMetadata.Error.Code)
	}
}

func TestCreateFaceVerificationAcceptsEmptyBody(t *testing.T) {
	setupAssetLibraryControllerTestDB(t)
	recorder, response := callAssetLibraryAction(t, "CreateFaceVerification", 1, "")

	// The request is valid; it fails later because no channel supports the
	// workflow, not because the body was empty.
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotNil(t, response.ResponseMetadata.Error)
	assert.Equal(t, "AssetLibraryUnavailable", response.ResponseMetadata.Error.Code)
}

func TestGetFaceVerificationIsScopedToTheOwningUser(t *testing.T) {
	db := setupAssetLibraryControllerTestDB(t)
	require.NoError(t, db.Create(&model.UserFaceVerification{
		UserId:         2,
		ChannelId:      10,
		VerificationId: "fv_owned_by_two",
		Status:         model.FaceVerificationStatusWaitingUser,
	}).Error)

	recorder, response := callAssetLibraryAction(t, "GetFaceVerification", 1, `{"VerificationId":"fv_owned_by_two"}`)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	require.NotNil(t, response.ResponseMetadata.Error)
	assert.Equal(t, "NotFound.VerificationId", response.ResponseMetadata.Error.Code)
}

func TestGetFaceVerificationRejectsMissingId(t *testing.T) {
	setupAssetLibraryControllerTestDB(t)
	recorder, response := callAssetLibraryAction(t, "GetFaceVerification", 1, `{}`)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotNil(t, response.ResponseMetadata.Error)
	assert.Equal(t, "InvalidParameter.VerificationId", response.ResponseMetadata.Error.Code)
}

func TestGetFaceVerificationReturnsTerminalStateWithoutTouchingUpstream(t *testing.T) {
	db := setupAssetLibraryControllerTestDB(t)
	require.NoError(t, db.Create(&model.UserFaceVerification{
		UserId:         1,
		ChannelId:      999,
		VerificationId: "fv_expired_one",
		Status:         model.FaceVerificationStatusExpired,
		H5URL:          "https://h5.example.com/verify",
		ExpiresAt:      1783740000,
	}).Error)

	recorder, response := callAssetLibraryAction(t, "GetFaceVerification", 1, `{"VerificationId":"fv_expired_one"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload, err := common.Marshal(response.Result)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(payload, &result))
	assert.Equal(t, "fv_expired_one", result["VerificationId"])
	assert.Equal(t, model.FaceVerificationStatusExpired, result["Status"])
	// Channel ids are internal and must never leak to clients.
	assert.NotContains(t, result, "ChannelId")
	assert.NotContains(t, result, "channel_id")
}

func TestGetFaceVerificationReturnsClaimedLogicalGroupId(t *testing.T) {
	db := setupAssetLibraryControllerTestDB(t)
	require.NoError(t, db.Create(&model.UserFaceVerification{
		UserId:         1,
		ChannelId:      10,
		VerificationId: "fv_verified_one",
		Status:         model.FaceVerificationStatusVerified,
		GroupId:        "group-na-0123456789abcdef0123456789abcdef",
	}).Error)

	recorder, response := callAssetLibraryAction(t, "GetFaceVerification", 1, `{"VerificationId":"fv_verified_one"}`)

	require.Equal(t, http.StatusOK, recorder.Code)
	payload, err := common.Marshal(response.Result)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(payload, &result))
	assert.Equal(t, "group-na-0123456789abcdef0123456789abcdef", result["GroupId"])
}
