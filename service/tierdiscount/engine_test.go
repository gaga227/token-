package tierdiscount

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTierTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Log{},
		&model.TierDiscountRule{}, &model.TierDiscountProgress{},
		&model.TierDiscountRebate{}, &model.UserQuotaLedger{},
	))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func mustCreateUser(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Create(&model.User{Id: id, Username: fmt.Sprintf("u%d", id)}).Error)
}

func mustRule(t *testing.T, db *gorm.DB, userId int, group, modelName string, thresholdCents int64, discount float64, buffer float64) {
	t.Helper()
	require.NoError(t, db.Create(&model.TierDiscountRule{
		UserId: userId, GroupName: group, Model: modelName,
		ThresholdCents: thresholdCents, Discount: discount, BufferRatio: buffer, Enabled: true,
	}).Error)
}

func getProgress(t *testing.T, db *gorm.DB, userId int, group, modelName string) model.TierDiscountProgress {
	t.Helper()
	var p model.TierDiscountProgress
	require.NoError(t, db.Where("user_id = ? AND group_name = ? AND model = ?", userId, group, modelName).First(&p).Error)
	return p
}

func getUserQuota(t *testing.T, db *gorm.DB, id int) int {
	t.Helper()
	var u model.User
	require.NoError(t, db.Select("quota").Where("id = ?", id).First(&u).Error)
	return u.Quota
}

// 核心链路：0 档首笔即 9 折 → 消费到 100 元触发 8 折并返还差额 → 再触发同档幂等不重复返。
func TestOnChargeTierUpgradeAndRebate(t *testing.T) {
	db := setupTierTestDB(t)
	mustCreateUser(t, db, 1)
	mustRule(t, db, 1, "vip", "seedance-2-0", 0, 0.9, 0)
	mustRule(t, db, 1, "vip", "seedance-2-0", 10000, 0.8, 0) // 100 元 → 8 折
	mustRule(t, db, 1, "vip", "seedance-2-0", 20000, 0.7, 0) // 200 元 → 7 折

	// 现金充值 1000 元
	require.NoError(t, model.RecordTierLedgerCharge(1, model.CentsToQuota(100000), true))

	// 首笔：0 档立即生效
	assert.Equal(t, 0.9, GetCurrentDiscount(1, "vip", "seedance-2-0"))

	// 消费 60 元（未到 100 元档）
	require.NoError(t, OnCharge(1, "vip", "seedance-2-0", 6000))
	p := getProgress(t, db, 1, "vip", "seedance-2-0")
	assert.Equal(t, int64(6000), p.TotalCents)
	assert.Equal(t, int64(6000), p.CashCents)
	assert.Equal(t, int64(0), p.CurrentThresholdCents)
	assert.Equal(t, 0.9, p.CurrentDiscount)

	// 再消费 50 元 → 累计 110 元，跨过 100 元档
	require.NoError(t, OnCharge(1, "vip", "seedance-2-0", 5000))
	p = getProgress(t, db, 1, "vip", "seedance-2-0")
	assert.Equal(t, int64(10000), p.CurrentThresholdCents)
	assert.Equal(t, 0.8, p.CurrentDiscount)
	assert.Equal(t, int64(10000), p.LastRebatedThresholdCents)

	// 返还 = 现金累计 110 元 × (0.9 − 0.8) = 11 元 → quota
	expectQuota := model.CentsToQuota(1100)
	assert.Equal(t, expectQuota, getUserQuota(t, db, 1))
	var rebates []model.TierDiscountRebate
	require.NoError(t, db.Find(&rebates).Error)
	require.Len(t, rebates, 1)
	assert.Equal(t, int64(1100), rebates[0].RebateCents)
	assert.Equal(t, expectQuota, rebates[0].RebateQuota)
	// 用户可见日志
	var logCnt int64
	db.Model(&model.Log{}).Where("user_id = ? AND type = ?", 1, model.LogTypeSystem).Count(&logCnt)
	assert.Equal(t, int64(1), logCnt)

	// 幂等：再来一笔小额消费，不应重复触发同档返还
	require.NoError(t, OnCharge(1, "vip", "seedance-2-0", 100))
	assert.Equal(t, expectQuota, getUserQuota(t, db, 1))
	require.NoError(t, db.Find(&rebates).Error)
	assert.Len(t, rebates, 1)

	// 消费到 210 元 → 跨 200 元档，返还 = 210 × (0.8−0.7) = 21 元
	require.NoError(t, OnCharge(1, "vip", "seedance-2-0", 9900))
	expectQuota2 := expectQuota + model.CentsToQuota(2100)
	assert.Equal(t, expectQuota2, getUserQuota(t, db, 1))
	require.NoError(t, db.Order("id").Find(&rebates).Error)
	require.Len(t, rebates, 2)
	assert.Equal(t, int64(2100), rebates[1].RebateCents)
	assert.Equal(t, 0.8, rebates[1].OldDiscount)
	assert.Equal(t, 0.7, rebates[1].NewDiscount)
}

// 赠送优先口径：赠送消费计入升档累计，但返现基数只算现金部分。
func TestGiftFirstSplitAffectsRebateBase(t *testing.T) {
	db := setupTierTestDB(t)
	mustCreateUser(t, db, 2)
	mustRule(t, db, 2, "g", "m", 10000, 0.75, 0) // 100 元 → 75 折（首档非 0，未达档按原价）

	// 先送 60 元赠送额度，再充 100 元现金
	require.NoError(t, model.RecordTierLedgerCharge(2, model.CentsToQuota(6000), false))
	require.NoError(t, model.RecordTierLedgerCharge(2, model.CentsToQuota(10000), true))

	// 未达档按原价
	assert.Equal(t, 1.0, GetCurrentDiscount(2, "g", "m"))

	// 消费 110 元（其中 60 元赠送 + 50 元现金）→ 累计 110 ≥ 100 触发升档
	require.NoError(t, OnCharge(2, "g", "m", 11000))
	p := getProgress(t, db, 2, "g", "m")
	assert.Equal(t, int64(11000), p.TotalCents) // 升档看全额
	// quota 是系统最小货币单位，分→quota→分往返存在 ±1 分截断，属固有精度
	assert.InDelta(t, 5000, p.CashCents, 1) // 现金部分约 50 元

	// 返还 ≈ 现金 50 元 × (1.0 − 0.75) = 12.5 元（不是 110 × 0.25）
	expectQuota := model.CentsToQuota(1250)
	assert.InDelta(t, expectQuota, getUserQuota(t, db, 2), 1400) // ±2 分 quota 粒度
	var rebates []model.TierDiscountRebate
	require.NoError(t, db.Find(&rebates).Error)
	require.Len(t, rebates, 1)
	assert.InDelta(t, 1250, rebates[0].RebateCents, 1)
}

// 滞后缓冲：buffer=0.1 时 100 元整不触发，110 元才确认档位。
func TestBufferRatioDelaysTierConfirmation(t *testing.T) {
	db := setupTierTestDB(t)
	mustCreateUser(t, db, 3)
	mustRule(t, db, 3, "g", "m", 10000, 0.8, 0.1)

	require.NoError(t, model.RecordTierLedgerCharge(3, model.CentsToQuota(20000), true))

	// 累计 100 元整：100 < 100×1.1=110，不确认
	require.NoError(t, OnCharge(3, "g", "m", 10000))
	p := getProgress(t, db, 3, "g", "m")
	assert.Equal(t, int64(0), p.CurrentThresholdCents)
	assert.Equal(t, 1.0, p.CurrentDiscount)
	assert.Equal(t, 0, getUserQuota(t, db, 3))

	// 累计 110 元：确认升档
	require.NoError(t, OnCharge(3, "g", "m", 1000))
	p = getProgress(t, db, 3, "g", "m")
	assert.Equal(t, int64(10000), p.CurrentThresholdCents)
	assert.Equal(t, 0.8, p.CurrentDiscount)
	// 返还 = 110 × (1.0−0.8) = 22 元
	assert.Equal(t, model.CentsToQuota(2200), getUserQuota(t, db, 3))
}

// 退款只减累计、不回退已触发档位。
func TestRefundReducesTotalWithoutDemotion(t *testing.T) {
	db := setupTierTestDB(t)
	mustCreateUser(t, db, 4)
	mustRule(t, db, 4, "g", "m", 10000, 0.8, 0)
	require.NoError(t, model.RecordTierLedgerCharge(4, model.CentsToQuota(20000), true))

	require.NoError(t, OnCharge(4, "g", "m", 11000))
	p := getProgress(t, db, 4, "g", "m")
	assert.Equal(t, 0.8, p.CurrentDiscount)

	// 退 50 元 → 累计回落到 60 元，但档位不回退
	require.NoError(t, OnRefund(4, "g", "m", 5000))
	p = getProgress(t, db, 4, "g", "m")
	assert.Equal(t, int64(6000), p.TotalCents)
	assert.Equal(t, int64(10000), p.CurrentThresholdCents)
	assert.Equal(t, 0.8, p.CurrentDiscount)
	assert.Equal(t, 0.8, GetCurrentDiscount(4, "g", "m"))
}

// 无规则 scope：零开销短路。
func TestNoRulesNoop(t *testing.T) {
	setupTierTestDB(t)
	assert.False(t, HasRules(99, "g", "m"))
	assert.Equal(t, 1.0, GetCurrentDiscount(99, "g", "m"))
	require.NoError(t, OnCharge(99, "g", "m", 10000))
}
