package model

import (
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 阶梯折扣（2026-09-23 定稿 · 扣款即折扣版）
//
// 口径约定：
//   - 维度 = 用户 × 渠道 × 模型（各渠道独立累计，不做通配）。
//   - 累计口径 = 折后实付 quota 净额（消费 type=2 累加、退款 type=6 冲减）。
//   - 档位判断 = 扣费时读当月汇总 1 行 + 比配置表取「阈值 ≤ 累计」的最高档。
//   - 跨档不追溯：跨档后下一笔起按新档扣，本月之前按旧档多扣的不退。
//   - 跨月自动归零：月份键是查询条件的一部分，不需要重置任务。
//   - 未配置（或全部停用）规则的身份一律原价扣费，无默认折扣。
//
// 表一 tier_rules：档位配置表（运营维护，一个身份多行构成阶梯）。
// 表二 tier_usage_monthly：月度汇总表（系统维护，一个身份一个月一行），
//   兼作「用户×渠道×模型」消耗报表的数据源。
// ============================================================

// tierLoc 月份键统一按中国标准时间计算，避免服务器时区导致跨月边界错账。
var tierLoc = time.FixedZone("CST", 8*3600)

// TierDiscountMonth 返回当前月份键（2006-01，固定 Asia/Shanghai 时区）。
func TierDiscountMonth() string {
	return time.Now().In(tierLoc).Format("2006-01")
}

// tierMonthRange 返回月份键对应的时间戳区间 [start, end)。
func tierMonthRange(month string) (int64, int64) {
	start, err := time.ParseInLocation("2006-01", month, tierLoc)
	if err != nil {
		// 非法月份键回退当前月，避免查出全表。
		now := time.Now().In(tierLoc)
		cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tierLoc)
		return cur.Unix(), cur.AddDate(0, 1, 0).Unix()
	}
	return start.Unix(), start.AddDate(0, 1, 0).Unix()
}

// RmbToTierQuota 人民币元 → quota（按充值价 operation_setting.Price 换算，
// 与用户余额同尺度：¥1 ≈ QuotaPerUnit / Price）。
func RmbToTierQuota(rmb float64) int64 {
	if rmb <= 0 {
		return 0
	}
	return int64(rmb * common.QuotaPerUnit / operation_setting.Price)
}

// TierQuotaToRmb 是 RmbToTierQuota 的展示用逆运算（quota → 元）。
// 结果四舍五入到「分」：RmbToTierQuota 有 int64 截断，直接逆算会得到
// 9.999992599999999 这类脏值，前端输入框/展示都需要干净的分值。
func TierQuotaToRmb(quota int64) float64 {
	rmb := float64(quota) * operation_setting.Price / common.QuotaPerUnit
	return math.Round(rmb*100) / 100
}

// TierRule 阶梯折扣档位配置（表 tier_rules）。
// 同一 (用户,渠道,模型) 配多行、按 ThresholdQuota 升序构成阶梯。
type TierRule struct {
	Id             int     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int     `json:"user_id" gorm:"index:idx_tier_rules_scope,unique,priority:1"`
	ChannelId      int     `json:"channel_id" gorm:"index:idx_tier_rules_scope,unique,priority:2"`
	Model          string  `json:"model" gorm:"type:varchar(128);index:idx_tier_rules_scope,unique,priority:3"`
	ThresholdQuota int64   `json:"threshold_quota" gorm:"index:idx_tier_rules_scope,unique,priority:4"` // 累计实耗达到该额度进入本档
	Discount       float64 `json:"discount"`                                                            // 如 0.92 = 92 折
	Enabled        bool    `json:"enabled"`                                                             // 启用/停用（不用 default:true：gorm 会吞掉显式 false）
	Remark         string  `json:"remark" gorm:"type:varchar(255);default:''"`
	CreatedTime    int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime    int64   `json:"updated_time" gorm:"bigint"`
	// ThresholdRmb 展示用（不入库）：ThresholdQuota 按充值价换算成元，避免前端
	// 用 USDExchangeRate（显示汇率）换算导致 7.68% 偏差。
	ThresholdRmb float64 `json:"threshold_rmb" gorm:"-"`
}

// TierUsageMonthly 月度消耗汇总（表 tier_usage_monthly）。
// 唯一键 (用户,渠道,模型,月份)：并发不重复插行、跨月自动分家、报表按月取数。
type TierUsageMonthly struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId        int    `json:"user_id" gorm:"index:idx_tier_usage_scope,unique,priority:1"`
	ChannelId     int    `json:"channel_id" gorm:"index:idx_tier_usage_scope,unique,priority:2"`
	Model         string `json:"model" gorm:"type:varchar(128);index:idx_tier_usage_scope,unique,priority:3"`
	Month         string `json:"month" gorm:"type:varchar(7);index:idx_tier_usage_scope,unique,priority:4"` // 2006-01
	ConsumedQuota int64  `json:"consumed_quota"` // 当月净实耗（消费−退款）
	CreatedTime   int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime   int64  `json:"updated_time" gorm:"bigint"`
	// ConsumedRmb 展示用（不入库）：按充值价换算成元。
	ConsumedRmb float64 `json:"consumed_rmb" gorm:"-"`
}

// HasTierRules 快速判断该身份是否配置了启用的档位规则（无规则时计费链路零开销短路）。
func HasTierRules(userId, channelId int, modelName string) bool {
	if userId <= 0 || modelName == "" {
		return false
	}
	var cnt int64
	DB.Model(&TierRule{}).
		Where("user_id = ? AND channel_id = ? AND model = ? AND enabled = ?",
			userId, channelId, modelName, true).
		Count(&cnt)
	return cnt > 0
}

// GetTierDiscount 返回该身份当前应使用的计费折扣（1.0 = 原价）。
// 读当月汇总 1 行 + 比配置表，无任何写操作；无规则/未达档返回 1.0。
func GetTierDiscount(userId, channelId int, modelName string) float64 {
	var rules []TierRule
	if err := DB.Where("user_id = ? AND channel_id = ? AND model = ? AND enabled = ?",
		userId, channelId, modelName, true).
		Order("threshold_quota asc").Find(&rules).Error; err != nil || len(rules) == 0 {
		return 1.0
	}
	var consumed int64
	var usage TierUsageMonthly
	if err := DB.Where("user_id = ? AND channel_id = ? AND model = ? AND month = ?",
		userId, channelId, modelName, TierDiscountMonth()).
		First(&usage).Error; err == nil {
		consumed = usage.ConsumedQuota
	}
	// 规则按阈值升序：取「阈值 ≤ 累计」的最后一行（即最高档）。
	discount := 1.0
	for _, r := range rules {
		if consumed >= r.ThresholdQuota && r.Discount > 0 && r.Discount <= 1.0 {
			discount = r.Discount
		}
	}
	return discount
}

// AdjustTierUsage 原子累加/冲减当月汇总（消费 +、退款 −）。
// 挂在日志写入出口调用；upsert 保证并发安全与首笔建行。
func AdjustTierUsage(userId, channelId int, modelName string, deltaQuota int64) error {
	if userId <= 0 || modelName == "" || deltaQuota == 0 {
		return nil
	}
	now := time.Now().Unix()
	row := TierUsageMonthly{
		UserId: userId, ChannelId: channelId, Model: modelName,
		Month: TierDiscountMonth(), ConsumedQuota: deltaQuota,
		CreatedTime: now, UpdatedTime: now,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"}, {Name: "channel_id"}, {Name: "model"}, {Name: "month"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"consumed_quota": gorm.Expr("consumed_quota + ?", deltaQuota),
			"updated_time":   now,
		}),
	}).Create(&row).Error
}

// SetTierUsageAbsolute 把某身份某月的汇总设置为绝对值（对账重算用）。
func SetTierUsageAbsolute(userId, channelId int, modelName, month string, consumedQuota int64) error {
	now := time.Now().Unix()
	row := TierUsageMonthly{
		UserId: userId, ChannelId: channelId, Model: modelName,
		Month: month, ConsumedQuota: consumedQuota,
		CreatedTime: now, UpdatedTime: now,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"}, {Name: "channel_id"}, {Name: "model"}, {Name: "month"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"consumed_quota": consumedQuota,
			"updated_time":   now,
		}),
	}).Create(&row).Error
}

// RecalcTierUsageScope 从日志重算某身份某月的净实耗并写回汇总表（对账口径：
// 日志即真相源）。只修数、不发钱。
func RecalcTierUsageScope(userId, channelId int, modelName, month string) (int64, error) {
	start, end := tierMonthRange(month)
	var net int64
	err := LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(CASE WHEN type = ? THEN quota WHEN type = ? THEN -quota ELSE 0 END), 0)",
			LogTypeConsume, LogTypeRefund).
		Where("user_id = ? AND channel_id = ? AND model_name = ? AND created_at >= ? AND created_at < ?",
			userId, channelId, modelName, start, end).
		Row().Scan(&net)
	if err != nil {
		return 0, err
	}
	if err := SetTierUsageAbsolute(userId, channelId, modelName, month, net); err != nil {
		return net, err
	}
	return net, nil
}

// TierRuleScopeList 列出已配置档位规则的全部身份（去重），供回填/对账遍历。
func TierRuleScopeList() ([]TierRule, error) {
	var scopes []TierRule
	err := DB.Select("DISTINCT user_id, channel_id, model").
		Order("user_id, channel_id, model").
		Find(&scopes).Error
	return scopes, err
}

// BackfillTierUsageCurrentMonth 上线/启动时回填：对每个已配置规则的身份，
// 从日志重算当月累计写入汇总表，保证用户当月消费不丢、跨档判断立刻正确。
func BackfillTierUsageCurrentMonth() (int, error) {
	scopes, err := TierRuleScopeList()
	if err != nil {
		return 0, err
	}
	month := TierDiscountMonth()
	n := 0
	for _, s := range scopes {
		if _, err := RecalcTierUsageScope(s.UserId, s.ChannelId, s.Model, month); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
