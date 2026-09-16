package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ============ 阶梯折扣规则管理（admin） ============

// GetTierDiscountRules 规则列表，支持 user_id/group_name/model 过滤。
func GetTierDiscountRules(c *gin.Context) {
	q := model.DB.Model(&model.TierDiscountRule{}).Order("user_id, group_name, model, threshold_cents")
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if v := c.Query("group_name"); v != "" {
		q = q.Where("group_name = ?", v)
	}
	if v := c.Query("model"); v != "" {
		q = q.Where("model = ?", v)
	}
	var rules []model.TierDiscountRule
	if err := q.Find(&rules).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rules})
}

// tierDiscountRuleInput 规则创建/更新入参（金额为元，入库转分）。
type tierDiscountRuleInput struct {
	UserId         int     `json:"user_id" binding:"required"`
	GroupName      string  `json:"group_name" binding:"required"`
	Model          string  `json:"model" binding:"required"`
	ThresholdRmb   float64 `json:"threshold_rmb"` // 累计达到（元）
	Discount       float64 `json:"discount" binding:"required"`
	BufferRatio    float64 `json:"buffer_ratio"`
	Enabled        bool    `json:"enabled"`
}

func (in *tierDiscountRuleInput) validate() string {
	if in.Discount <= 0 || in.Discount > 1 {
		return "折扣必须在 (0, 1] 区间（1 表示原价）"
	}
	if in.ThresholdRmb < 0 {
		return "阈值不能为负"
	}
	if in.BufferRatio < 0 || in.BufferRatio > 1 {
		return "滞后缓冲比例须在 [0, 1] 区间"
	}
	return ""
}

// CreateTierDiscountRule 新增规则。
func CreateTierDiscountRule(c *gin.Context) {
	var in tierDiscountRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数不完整: " + err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	rule := model.TierDiscountRule{
		UserId:         in.UserId,
		GroupName:      in.GroupName,
		Model:          in.Model,
		ThresholdCents: int64(in.ThresholdRmb * 100),
		Discount:       in.Discount,
		BufferRatio:    in.BufferRatio,
		Enabled:        in.Enabled,
		CreatedTime:    time.Now().Unix(),
		UpdatedTime:    time.Now().Unix(),
	}
	if err := model.DB.Create(&rule).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建失败（同用户/分组/模型/阈值重复？）: " + err.Error()})
		return
	}
	model.RecordOperationAuditLog(0, "创建阶梯折扣规则", c.ClientIP(), "create", map[string]interface{}{
		"user_id": rule.UserId, "group": rule.GroupName, "model": rule.Model,
		"threshold_rmb": in.ThresholdRmb, "discount": rule.Discount, "rule_id": rule.Id,
	}, nil, nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rule})
}

// UpdateTierDiscountRule 更新规则（阈值/折扣/缓冲/开关）。
func UpdateTierDiscountRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "非法 id"})
		return
	}
	var in tierDiscountRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数不完整: " + err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	var rule model.TierDiscountRule
	if err := model.DB.First(&rule, id).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "规则不存在"})
		return
	}
	rule.ThresholdCents = int64(in.ThresholdRmb * 100)
	rule.Discount = in.Discount
	rule.BufferRatio = in.BufferRatio
	rule.Enabled = in.Enabled
	rule.UpdatedTime = time.Now().Unix()
	if err := model.DB.Save(&rule).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rule})
}

// DeleteTierDiscountRule 删除规则。
func DeleteTierDiscountRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "非法 id"})
		return
	}
	if err := model.DB.Delete(&model.TierDiscountRule{}, id).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetTierDiscountProgress 用户当月进度查询。
func GetTierDiscountProgress(c *gin.Context) {
	q := model.DB.Model(&model.TierDiscountProgress{}).Order("updated_time desc")
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if v := c.Query("month"); v != "" {
		q = q.Where("month = ?", v)
	}
	var list []model.TierDiscountProgress
	if err := q.Limit(200).Find(&list).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetTierDiscountRebates 返还记录查询。
func GetTierDiscountRebates(c *gin.Context) {
	q := model.DB.Model(&model.TierDiscountRebate{}).Order("created_time desc")
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if v := c.Query("month"); v != "" {
		q = q.Where("month = ?", v)
	}
	var list []model.TierDiscountRebate
	if err := q.Limit(200).Find(&list).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}
