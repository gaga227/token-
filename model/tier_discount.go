package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
)

// QuotaToCents 把 quota 折算为人民币分（系统统一口径：
// quota / QuotaPerUnit × USDExchangeRate × 100）。
func QuotaToCents(quota int) int64 {
	if quota <= 0 {
		return 0
	}
	return int64(float64(quota) / common.QuotaPerUnit * operation_setting.USDExchangeRate * 100)
}

// CentsToQuota 是 QuotaToCents 的逆运算（人民币分 → quota）。
func CentsToQuota(cents int64) int {
	if cents <= 0 {
		return 0
	}
	return int(float64(cents) / 100 / operation_setting.USDExchangeRate * common.QuotaPerUnit)
}

// getOrCreateTierLedger 事务内取/建用户账本（PG/MySQL 加行锁，SQLite 跳过）。
func getOrCreateTierLedger(tx *gorm.DB, userId int) (*UserQuotaLedger, error) {
	var ledger UserQuotaLedger
	err := LockForUpdate(tx).Where("user_id = ?", userId).First(&ledger).Error
	if err == gorm.ErrRecordNotFound {
		ledger = UserQuotaLedger{UserId: userId, UpdatedTime: time.Now().Unix()}
		if err := tx.Create(&ledger).Error; err != nil {
			return nil, err
		}
		return &ledger, nil
	}
	if err != nil {
		return nil, err
	}
	return &ledger, nil
}

// RecordTierLedgerCharge 在用户余额增加时记现金/赠送账本（阶梯折扣返现基数用）。
// isCash=true：付费充值/卡密兑换；false：管理员赠送、注册/邀请奖励等系统赠送。
// 账本只记账、不动 quota 本体。
func RecordTierLedgerCharge(userId int, quota int, isCash bool) error {
	if userId <= 0 || quota <= 0 {
		return nil
	}
	cents := QuotaToCents(quota)
	if cents <= 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		ledger, err := getOrCreateTierLedger(tx, userId)
		if err != nil {
			return err
		}
		if isCash {
			ledger.CashChargedCents += cents
		} else {
			ledger.GiftChargedCents += cents
		}
		ledger.UpdatedTime = time.Now().Unix()
		return tx.Save(ledger).Error
	})
}

// SplitTierChargeCents 事务内把一笔消费（折前分）按「赠送优先」口径拆分并记账，
// 返回现金部分（返现基数用）。供 service 层引擎在自身事务里复用——注意调用方
// 事务与本函数共用 tx，不会嵌套开新事务。
func SplitTierChargeCents(tx *gorm.DB, userId int, chargeCents int64) (cashCents int64, err error) {
	if chargeCents <= 0 {
		return 0, nil
	}
	ledger, err := getOrCreateTierLedger(tx, userId)
	if err != nil {
		return 0, err
	}
	giftRemain := ledger.GiftChargedCents - ledger.GiftConsumedCents
	if giftRemain < 0 {
		giftRemain = 0
	}
	giftPart := chargeCents
	if giftPart > giftRemain {
		giftPart = giftRemain
	}
	cashCents = chargeCents - giftPart
	ledger.GiftConsumedCents += giftPart
	ledger.CashConsumedCents += cashCents
	ledger.UpdatedTime = time.Now().Unix()
	return cashCents, tx.Save(ledger).Error
}

//
// 业务语义（2026-09-10 与用户确认）：
//   - 规则按「用户 × 分组（线路）× 对外模型名」配置：当月累计消费（折前人民币）
//     达到 ThresholdCents 后按 Discount 扣费；缺档沿用最近低档；未达最低档按原价。
//   - 跨档触发带滞后缓冲（BufferRatio，如 0.1 表示超过阈值 10% 才确认），防止
//     视频任务预扣/退款造成的累计抖动反复触发。
//   - 档位只升不回退；跨档瞬间的「骑线请求」整笔按旧档扣费，追溯重算时统一。
//   - 跨档确认后即时返还：返还 = 当月现金累计 × (旧折 − 新折)，写审计表并
//     给用户记一条 LogTypeSystem 日志。同一 user×group×model×month×tier 幂等。
//   - 金额内部一律用「分」（int64）存储，避免浮点累计误差；quota↔分 的换算
//     使用系统统一口径 quota/QuotaPerUnit×USDExchangeRate×100。

// TierDiscountRule 阶梯折扣规则。
type TierDiscountRule struct {
	Id              int     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId          int     `json:"user_id" gorm:"index:idx_tdr_scope,unique"`
	GroupName       string  `json:"group_name" gorm:"type:varchar(64);index:idx_tdr_scope,unique"`
	Model           string  `json:"model" gorm:"type:varchar(128);index:idx_tdr_scope,unique"`
	ThresholdCents  int64   `json:"threshold_cents" gorm:"index:idx_tdr_scope,unique"`
	Discount        float64 `json:"discount"`
	BufferRatio     float64 `json:"buffer_ratio" gorm:"default:0"`
	Enabled         bool    `json:"enabled" gorm:"default:true"`
	CreatedTime     int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime     int64   `json:"updated_time" gorm:"bigint"`
}

// TierDiscountProgress 用户当月某「分组×模型」的累计进度（幂等锁在这里）。
type TierDiscountProgress struct {
	Id                        int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId                    int     `json:"user_id" gorm:"index:idx_tdp_scope,unique"`
	GroupName                 string  `json:"group_name" gorm:"type:varchar(64);index:idx_tdp_scope,unique"`
	Model                     string  `json:"model" gorm:"type:varchar(128);index:idx_tdp_scope,unique"`
	Month                     string  `json:"month" gorm:"type:varchar(7);index:idx_tdp_scope,unique"` // 2006-01
	TotalCents                int64   `json:"total_cents"`                                           // 折前累计（含赠送），升档依据
	CashCents                 int64   `json:"cash_cents"`                                            // 折前现金累计，返现基数
	CurrentThresholdCents     int64   `json:"current_threshold_cents"`                               // 当前已确认档位阈值
	CurrentDiscount           float64 `json:"current_discount"`                                      // 当前计费折扣（1.0=原价）
	LastRebatedThresholdCents int64   `json:"last_rebated_threshold_cents"`                          // 已返还至的档位（幂等）
	UpdatedTime               int64   `json:"updated_time" gorm:"bigint"`
}

// TierDiscountRebate 跨档返还审计日志。
type TierDiscountRebate struct {
	Id                  int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId              int     `json:"user_id" gorm:"index"`
	GroupName           string  `json:"group_name" gorm:"type:varchar(64)"`
	Model               string  `json:"model" gorm:"type:varchar(128)"`
	Month               string  `json:"month" gorm:"type:varchar(7)"`
	FromThresholdCents  int64   `json:"from_threshold_cents"`
	ToThresholdCents    int64   `json:"to_threshold_cents"`
	BaseCents           int64   `json:"base_cents"` // 返现基数（当月现金累计）
	OldDiscount         float64 `json:"old_discount"`
	NewDiscount         float64 `json:"new_discount"`
	RebateCents         int64   `json:"rebate_cents"`
	RebateQuota         int     `json:"rebate_quota"`
	Status              string  `json:"status" gorm:"type:varchar(16);default:'success'"`
	CreatedTime         int64   `json:"created_time" gorm:"bigint"`
}

// UserQuotaLedger 用户余额的现金/赠送拆分账本（quota 单池不动，仅记账）。
// 赠送优先口径：消费先计赠送消耗，赠送耗尽后才计现金消耗——升档累计看全额，
// 返现基数只看现金部分，避免赠送额度被用来薅返现。
type UserQuotaLedger struct {
	UserId            int   `json:"user_id" gorm:"primaryKey"`
	CashChargedCents  int64 `json:"cash_charged_cents"`  // 累计现金充值（付费充值/卡密）
	CashConsumedCents int64 `json:"cash_consumed_cents"` // 累计现金消耗
	GiftChargedCents  int64 `json:"gift_charged_cents"`  // 累计赠送（管理员赠送/注册邀请奖励）
	GiftConsumedCents int64 `json:"gift_consumed_cents"` // 累计赠送消耗
	UpdatedTime       int64 `json:"updated_time" gorm:"bigint"`
}
