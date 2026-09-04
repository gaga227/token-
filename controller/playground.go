package controller

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// preparePlaygroundSession 校验 dashboard 会话并伪造 playground 临时 token 上下文，
// 返回预检用的 relayInfo。失败时返回 newAPIError（尚未写响应）。
// relayFormat 决定预检时生成的 RelayInfo 形态：聊天走 RelayFormatOpenAI，
// 视频任务（提交/查询）走 RelayFormatTask。
func preparePlaygroundSession(c *gin.Context, relayFormat types.RelayFormat) (*relaycommon.RelayInfo, *types.NewAPIError) {
	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		return nil, types.NewError(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, nil, nil)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)

	return relayInfo, nil
}

func Playground(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	relayInfo, err := preparePlaygroundSession(c, types.RelayFormatOpenAI)
	if err != nil {
		newAPIError = err
		return
	}
	_ = relayInfo

	Relay(c, types.RelayFormatOpenAI)
}

// PlaygroundVideoTask 供 dashboard 会话以 playground 身份提交视频任务，
// 行为等价 POST /v1/videos（含 OpenAI 兼容 JSON 请求体与 URL 素材）。
func PlaygroundVideoTask(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	relayInfo, err := preparePlaygroundSession(c, types.RelayFormatTask)
	if err != nil {
		newAPIError = err
		return
	}
	_ = relayInfo

	RelayTask(c)
}

// PlaygroundVideoTaskFetch 供 dashboard 会话查询视频任务，
// 行为等价 GET /v1/videos/{task_id}（返回 OpenAI 格式含 consumed_* 字段）。
func PlaygroundVideoTaskFetch(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	relayInfo, err := preparePlaygroundSession(c, types.RelayFormatTask)
	if err != nil {
		newAPIError = err
		return
	}
	_ = relayInfo

	RelayTaskFetch(c)
}
