// Package tierdiscount 实现「用户×渠道×模型」阶梯折扣（2026-09-23 定稿 · 扣款即折扣版）。
//
// 计费链路在扣费前调用 HasRules / GetCurrentDiscount 取当前档折扣并乘到应扣额上；
// 当月累计由日志写入出口（model.RecordConsumeLog / model.RecordTaskBillingLog）
// 原子维护在 tier_usage_monthly 表，本包不再有任何累加/返还逻辑。
//
// 口径：累计 = 折后实付净额；未配置规则一律原价；跨月自动归零；跨档不追溯。
package tierdiscount

import "github.com/QuantumNous/new-api/model"

// HasRules 该身份（用户×渠道×模型）是否配置了启用的档位规则。
// 无规则时计费链路据此短路，零额外开销。
func HasRules(userId, channelId int, modelName string) bool {
	return model.HasTierRules(userId, channelId, modelName)
}

// GetCurrentDiscount 返回当前档折扣（1.0 = 原价）。
// 读当月汇总 1 行 + 比配置表，纯只读。
func GetCurrentDiscount(userId, channelId int, modelName string) float64 {
	return model.GetTierDiscount(userId, channelId, modelName)
}
