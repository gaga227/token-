package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ============ 阶梯折扣管理（admin，2026-09-23 定稿） ============
// 维度 = 用户 × 渠道 × 模型；阈值按元录入、按充值价换算 quota 存储。

// GetTierRules 规则列表，支持 user_id/channel_id/model 过滤。
func GetTierRules(c *gin.Context) {
	q := model.DB.Model(&model.TierRule{}).Order("user_id, channel_id, model, threshold_quota")
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if v := c.Query("channel_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("channel_id = ?", id)
		}
	}
	if v := c.Query("model"); v != "" {
		q = q.Where("model = ?", v)
	}
	var rules []model.TierRule
	if err := q.Find(&rules).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	for i := range rules {
		rules[i].ThresholdRmb = model.TierQuotaToRmb(rules[i].ThresholdQuota)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rules})
}

// tierRuleInput 规则创建/更新入参。ThresholdRmb 按元录入（阈值是「累计实耗」，
// 与余额共用 quota 尺度：¥1 ≈ QuotaPerUnit / Price）。
type tierRuleInput struct {
	UserId       int     `json:"user_id" binding:"required"`
	ChannelId    int     `json:"channel_id" binding:"required"`
	Model        string  `json:"model" binding:"required"`
	ThresholdRmb float64 `json:"threshold_rmb"` // 累计实耗达到（元），0 = 起步档
	Discount     float64 `json:"discount" binding:"required"`
	// Enabled 不传默认启用（指针区分「未传」与「显式 false」）
	Enabled *bool  `json:"enabled"`
	Remark  string `json:"remark"`
}

func (in *tierRuleInput) enabledOrDefault() bool {
	return in.Enabled == nil || *in.Enabled
}

func (in *tierRuleInput) validate() string {
	if in.Discount <= 0 || in.Discount > 1 {
		return "折扣必须在 (0, 1] 区间（1 表示原价）"
	}
	if in.ThresholdRmb < 0 {
		return "阈值不能为负"
	}
	if in.UserId <= 0 {
		return "用户 ID 非法"
	}
	if in.ChannelId <= 0 {
		return "渠道 ID 非法"
	}
	return ""
}

// CreateTierRule 新增档位。
func CreateTierRule(c *gin.Context) {
	var in tierRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数不完整: " + err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	rule := model.TierRule{
		UserId:         in.UserId,
		ChannelId:      in.ChannelId,
		Model:          in.Model,
		ThresholdQuota: model.RmbToTierQuota(in.ThresholdRmb),
		Discount:       in.Discount,
		Enabled:        in.enabledOrDefault(),
		Remark:         in.Remark,
		ThresholdRmb:   in.ThresholdRmb,
		CreatedTime:    time.Now().Unix(),
		UpdatedTime:    time.Now().Unix(),
	}
	if err := model.DB.Create(&rule).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建失败（同身份同阈值重复？）: " + err.Error()})
		return
	}
	model.RecordOperationAuditLog(0, "创建阶梯折扣档位", c.ClientIP(), "create", map[string]interface{}{
		"user_id": rule.UserId, "channel_id": rule.ChannelId, "model": rule.Model,
		"threshold_rmb": in.ThresholdRmb, "discount": rule.Discount, "rule_id": rule.Id,
	}, nil, nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rule})
}

// UpdateTierRule 更新档位（阈值/折扣/开关/备注）。
func UpdateTierRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "非法 id"})
		return
	}
	var in tierRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数不完整: " + err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	var rule model.TierRule
	if err := model.DB.First(&rule, id).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "档位不存在"})
		return
	}
	rule.ThresholdQuota = model.RmbToTierQuota(in.ThresholdRmb)
	rule.Discount = in.Discount
	rule.Enabled = in.enabledOrDefault()
	rule.Remark = in.Remark
	rule.UpdatedTime = time.Now().Unix()
	rule.ThresholdRmb = in.ThresholdRmb
	if err := model.DB.Save(&rule).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rule})
}

// DeleteTierRule 删除档位。已享折扣不收回（跨档不追溯的镜像语义）。
func DeleteTierRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "非法 id"})
		return
	}
	if err := model.DB.Delete(&model.TierRule{}, id).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetTierUsage 月度汇总查询（兼作「用户×渠道×模型 消耗总金额」报表数据源）。
// 支持 user_id/channel_id/model/month 过滤，month 缺省为当月。
func GetTierUsage(c *gin.Context) {
	month := c.Query("month")
	if month == "" {
		month = model.TierDiscountMonth()
	}
	q := model.DB.Model(&model.TierUsageMonthly{}).Where("month = ?", month).
		Order("consumed_quota desc")
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if v := c.Query("channel_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("channel_id = ?", id)
		}
	}
	if v := c.Query("model"); v != "" {
		q = q.Where("model = ?", v)
	}
	var list []model.TierUsageMonthly
	if err := q.Limit(1000).Find(&list).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	for i := range list {
		list[i].ConsumedRmb = model.TierQuotaToRmb(list[i].ConsumedQuota)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"month": month, "items": list}})
}

// RecalcTierUsage 手动触发对账重算：从日志重算某身份（或全部规则身份）当月汇总。
// 运维对账用；只修数、不发钱。
func RecalcTierUsage(c *gin.Context) {
	month := c.Query("month")
	if month == "" {
		month = model.TierDiscountMonth()
	}
	// 指定身份：单 scope 重算
	if v := c.Query("user_id"); v != "" {
		userId, err1 := strconv.Atoi(v)
		channelId, err2 := strconv.Atoi(c.Query("channel_id"))
		modelName := c.Query("model")
		if err1 != nil || err2 != nil || modelName == "" {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "指定身份重算需 user_id + channel_id + model 全部提供"})
			return
		}
		net, err := model.RecalcTierUsageScope(userId, channelId, modelName, month)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"month": month, "consumed_quota": net}})
		return
	}
	// 未指定身份：重算全部规则身份
	n, err := model.BackfillTierUsageCurrentMonth()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"month": month, "scopes": n}})
}
