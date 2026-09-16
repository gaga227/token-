// Package tierdiscount 实现「用户×分组×模型」阶梯折扣：
// 当月累计消费跨档后切换计费折扣、追溯重算并把差额返还到用户余额。
//
// 口径约定（2026-09-10 与用户确认）：
//   - 累计与阈值均为折前人民币（系统内一律用「分」int64）。
//   - 升档累计含赠送额度消费；返现基数只含现金部分（赠送优先消耗口径）。
//   - 档位只升不回退；骑线请求整笔按旧档扣；跨档触发支持滞后缓冲。
//   - 返还即时、幂等（progress.LastRebatedThresholdCents 为锁）。
package tierdiscount

import (
	"time"

	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

// QuotaToCents / CentsToQuota 换算下沉在 model 包（model.QuotaToCents），
// 此处不再重复实现。

func currentMonth() string {
	return time.Now().Format("2006-01")
}

// loadRules 取出某 scope 的全部启用规则，按阈值升序。
func loadRules(tx *gorm.DB, userId int, group, modelName string) ([]model.TierDiscountRule, error) {
	var rules []model.TierDiscountRule
	err := tx.Where("user_id = ? AND group_name = ? AND model = ? AND enabled = ?",
		userId, group, modelName, true).
		Order("threshold_cents asc").Find(&rules).Error
	return rules, err
}

// matchTier 返回「已确认」的最高档位：累计需达到 阈值×(1+缓冲) 才算确认。
// 无已确认档位时返回 nil（按原价计费）。
func matchTier(rules []model.TierDiscountRule, totalCents int64) *model.TierDiscountRule {
	var best *model.TierDiscountRule
	for i := range rules {
		r := &rules[i]
		confirmAt := r.ThresholdCents + int64(float64(r.ThresholdCents)*r.BufferRatio)
		if totalCents >= confirmAt && (best == nil || r.ThresholdCents > best.ThresholdCents) {
			best = r
		}
	}
	return best
}

// matchTierImmediate 按累计直接匹配最高档（不考虑缓冲），用于新 scope
// 首笔消费时的初始折扣（0 阈值档立即可达）。
func matchTierImmediate(rules []model.TierDiscountRule, totalCents int64) *model.TierDiscountRule {
	var best *model.TierDiscountRule
	for i := range rules {
		r := &rules[i]
		if totalCents >= r.ThresholdCents && (best == nil || r.ThresholdCents > best.ThresholdCents) {
			best = r
		}
	}
	return best
}

// GetCurrentDiscount 返回该 scope 当前应使用的计费折扣（1.0 = 原价）。
// 读路径不加锁；无进度记录时按累计 0 匹配规则（让 0 阈值档首笔即生效）。
func GetCurrentDiscount(userId int, group, modelName string) float64 {
	var prog model.TierDiscountProgress
	err := model.DB.Where("user_id = ? AND group_name = ? AND model = ? AND month = ?",
		userId, group, modelName, currentMonth()).First(&prog).Error
	if err == nil {
		return prog.CurrentDiscount
	}
	rules, err := loadRules(model.DB, userId, group, modelName)
	if err != nil || len(rules) == 0 {
		return 1.0
	}
	if r := matchTierImmediate(rules, 0); r != nil {
		return r.Discount
	}
	return 1.0
}

// HasRules 快速判断该 scope 是否配置了阶梯规则（无规则时计费链路零开销短路）。
func HasRules(userId int, group, modelName string) bool {
	var cnt int64
	model.DB.Model(&model.TierDiscountRule{}).
		Where("user_id = ? AND group_name = ? AND model = ? AND enabled = ?",
			userId, group, modelName, true).
		Count(&cnt)
	return cnt > 0
}

// OnCharge 在一笔消费净结算后调用。chargeCents 为本次消费的折前人民币分。
// 负责：现金/赠送拆分记账 → 当月累计 → 档位确认 → 必要时触发跨档返还。
// 全流程单事务；progress 行加锁（PG FOR UPDATE / SQLite 单写）。
func OnCharge(userId int, group, modelName string, chargeCents int64) error {
	if chargeCents <= 0 || userId <= 0 {
		return nil
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1) 现金/赠送拆分（赠送优先：消费先计赠送消耗）
		cashPart, err := model.SplitTierChargeCents(tx, userId, chargeCents)
		if err != nil {
			return err
		}

		// 2) 当月进度累计
		prog, err := getOrCreateProgress(tx, userId, group, modelName)
		if err != nil {
			return err
		}
		prog.TotalCents += chargeCents
		prog.CashCents += cashPart

		// 3) 档位确认（只升不回退）
		rules, err := loadRules(tx, userId, group, modelName)
		if err != nil {
			return err
		}
		target := matchTier(rules, prog.TotalCents)
		if target != nil && target.ThresholdCents > prog.CurrentThresholdCents {
			oldDiscount := prog.CurrentDiscount
			prog.CurrentThresholdCents = target.ThresholdCents
			prog.CurrentDiscount = target.Discount
			// 4) 跨档返还（幂等：只返还到从未返还过的更高档）
			if target.ThresholdCents > prog.LastRebatedThresholdCents && oldDiscount > target.Discount {
				if err := rebateLocked(tx, prog, oldDiscount, target); err != nil {
					return err
				}
			}
			if target.ThresholdCents > prog.LastRebatedThresholdCents {
				prog.LastRebatedThresholdCents = target.ThresholdCents
			}
		}
		prog.UpdatedTime = time.Now().Unix()
		return tx.Save(prog).Error
	})
}

// OnChargeQuota 是 OnCharge 的 quota 便捷包装。
func OnChargeQuota(userId int, group, modelName string, quota int) error {
	return OnCharge(userId, group, modelName, model.QuotaToCents(quota))
}

// OnRefund 在任务结算退款后调用：累计只减不回退档位。
// refundCents 为退还部分的折前人民币分（退款按同口径折算）。
func OnRefund(userId int, group, modelName string, refundCents int64) error {
	if refundCents <= 0 || userId <= 0 {
		return nil
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		prog, err := getOrCreateProgress(tx, userId, group, modelName)
		if err != nil {
			return err
		}
		if refundCents > prog.TotalCents {
			refundCents = prog.TotalCents
		}
		prog.TotalCents -= refundCents
		if refundCents > prog.CashCents {
			refundCents = prog.CashCents
		}
		prog.CashCents -= refundCents
		prog.UpdatedTime = time.Now().Unix()
		return tx.Save(prog).Error
	})
}

func getOrCreateProgress(tx *gorm.DB, userId int, group, modelName string) (*model.TierDiscountProgress, error) {
	var prog model.TierDiscountProgress
	err := model.LockForUpdate(tx).Where(
		"user_id = ? AND group_name = ? AND model = ? AND month = ?",
		userId, group, modelName, currentMonth()).First(&prog).Error
	if err == gorm.ErrRecordNotFound {
		prog = model.TierDiscountProgress{
			UserId: userId, GroupName: group, Model: modelName, Month: currentMonth(),
			CurrentDiscount: 1.0, UpdatedTime: time.Now().Unix(),
		}
		// 初始化即匹配 0 阈值档（若有），使首笔消费按规则折扣计。
		if rules, rerr := loadRules(tx, userId, group, modelName); rerr == nil {
			if r := matchTierImmediate(rules, 0); r != nil {
				prog.CurrentThresholdCents = r.ThresholdCents
				prog.CurrentDiscount = r.Discount
				prog.LastRebatedThresholdCents = r.ThresholdCents // 首档无返还意义
			}
		}
		if err := tx.Create(&prog).Error; err != nil {
			return nil, err
		}
		return &prog, nil
	}
	if err != nil {
		return nil, err
	}
	return &prog, nil
}
