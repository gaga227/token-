package tierdiscount

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTierTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Log{},
		&model.TierRule{}, &model.TierUsageMonthly{},
	))
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func mustRule(t *testing.T, userId, channelId int, modelName string, thresholdQuota int64, discount float64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.TierRule{
		UserId: userId, ChannelId: channelId, Model: modelName,
		ThresholdQuota: thresholdQuota, Discount: discount, Enabled: true,
	}).Error)
}

// 无规则身份：一律原价，零默认折扣（18:26 用户拍板的边界）。
func TestNoRulesOriginalPrice(t *testing.T) {
	setupTierTestDB(t)
	assert.False(t, HasRules(99, 1, "seedance-2-5"))
	assert.Equal(t, 1.0, GetCurrentDiscount(99, 1, "seedance-2-5"))
}

// 起步档（阈值 0）首笔即折扣；累计跨档后下一笔取新档。
func TestStartTierAndUpgrade(t *testing.T) {
	setupTierTestDB(t)
	// ¥1 ≈ 68493 quota，这里直接用 quota 值构造：起步 92 折，10M(约146元) 90 折
	mustRule(t, 1, 29, "seedance-2-5", 0, 0.92)
	mustRule(t, 1, 29, "seedance-2-5", 10_000_000, 0.90)

	// 首笔（累计 0）命中起步档
	assert.Equal(t, 0.92, GetCurrentDiscount(1, 29, "seedance-2-5"))

	// 累计 9M（约 131 元，未到下一档）
	require.NoError(t, model.AdjustTierUsage(1, 29, "seedance-2-5", 9_000_000))
	assert.Equal(t, 0.92, GetCurrentDiscount(1, 29, "seedance-2-5"))

	// 累计再 +2M 跨档 → 下一笔 90 折
	require.NoError(t, model.AdjustTierUsage(1, 29, "seedance-2-5", 2_000_000))
	assert.Equal(t, 0.90, GetCurrentDiscount(1, 29, "seedance-2-5"))
}

// 净额口径：消费累加、退款冲减；冲减后跌破阈值不升档（现算逻辑天然如此）。
func TestRefundReducesConsumed(t *testing.T) {
	setupTierTestDB(t)
	mustRule(t, 2, 11, "kimi-k3", 5_000_000, 0.85)

	// 未达档原价
	assert.Equal(t, 1.0, GetCurrentDiscount(2, 11, "kimi-k3"))

	require.NoError(t, model.AdjustTierUsage(2, 11, "kimi-k3", 6_000_000))
	assert.Equal(t, 0.85, GetCurrentDiscount(2, 11, "kimi-k3"))

	// 退 3M → 净额 3M，回到原价
	require.NoError(t, model.AdjustTierUsage(2, 11, "kimi-k3", -3_000_000))
	assert.Equal(t, 1.0, GetCurrentDiscount(2, 11, "kimi-k3"))
}

// 渠道维度独立：同用户同模型不同渠道各算各的（用户 17:20 拍板）。
func TestChannelScopesIndependent(t *testing.T) {
	setupTierTestDB(t)
	mustRule(t, 3, 29, "seedance-2-0", 1_000_000, 0.9)

	require.NoError(t, model.AdjustTierUsage(3, 29, "seedance-2-0", 2_000_000))
	// 渠道 30 无累计无规则
	assert.Equal(t, 1.0, GetCurrentDiscount(3, 30, "seedance-2-0"))
	// 渠道 29 已达档
	assert.Equal(t, 0.9, GetCurrentDiscount(3, 29, "seedance-2-0"))
}

// 停用的档位不参与选档；全部停用 = 原价。
func TestDisabledRulesIgnored(t *testing.T) {
	setupTierTestDB(t)
	require.NoError(t, model.DB.Create(&model.TierRule{
		UserId: 4, ChannelId: 1, Model: "m", ThresholdQuota: 0, Discount: 0.8, Enabled: false,
	}).Error)
	assert.False(t, HasRules(4, 1, "m"))
	assert.Equal(t, 1.0, GetCurrentDiscount(4, 1, "m"))
}

// 跨月自动归零：历史月行不参与当月选档。
func TestCrossMonthReset(t *testing.T) {
	setupTierTestDB(t)
	mustRule(t, 5, 1, "m", 0, 0.92)

	// 历史月行
	require.NoError(t, model.SetTierUsageAbsolute(5, 1, "m", "2000-01", 99_999_999))
	assert.Equal(t, 0.92, GetCurrentDiscount(5, 1, "m")) // 当月无行 → 命中 0 阈值起步档

	// 若当月累计为 0，大额历史不产生更高折扣
	require.NoError(t, model.DB.Create(&model.TierRule{
		UserId: 5, ChannelId: 1, Model: "m", ThresholdQuota: 50_000_000, Discount: 0.8, Enabled: true,
	}).Error)
	assert.Equal(t, 0.92, GetCurrentDiscount(5, 1, "m"))
}

// 日志重算对账：从 logs(type=2 加 / type=6 减) 重算出的净额与实时累加一致。
func TestRecalcFromLogs(t *testing.T) {
	db := setupTierTestDB(t)
	month := model.TierDiscountMonth()
	start, _ := monthStartForTest(month)

	logs := []model.Log{
		{UserId: 6, ChannelId: 29, ModelName: "m", Type: model.LogTypeConsume, Quota: 1_000_000, CreatedAt: start + 10},
		{UserId: 6, ChannelId: 29, ModelName: "m", Type: model.LogTypeConsume, Quota: 500_000, CreatedAt: start + 20},
		{UserId: 6, ChannelId: 29, ModelName: "m", Type: model.LogTypeRefund, Quota: 200_000, CreatedAt: start + 30},
		// 上月日志不应计入
		{UserId: 6, ChannelId: 29, ModelName: "m", Type: model.LogTypeConsume, Quota: 999_999, CreatedAt: start - 100},
		// 其他身份不混入
		{UserId: 7, ChannelId: 29, ModelName: "m", Type: model.LogTypeConsume, Quota: 888_888, CreatedAt: start + 40},
	}
	for i := range logs {
		require.NoError(t, db.Create(&logs[i]).Error)
	}

	net, err := model.RecalcTierUsageScope(6, 29, "m", month)
	require.NoError(t, err)
	assert.Equal(t, int64(1_300_000), net)

	// 实时累加路径（独立身份，不与重算行叠加）与重算结果一致
	require.NoError(t, model.AdjustTierUsage(9, 29, "m", 1_000_000))
	require.NoError(t, model.AdjustTierUsage(9, 29, "m", 500_000))
	require.NoError(t, model.AdjustTierUsage(9, 29, "m", -200_000))
	var usage model.TierUsageMonthly
	require.NoError(t, model.DB.Where("user_id = 9 AND channel_id = 29 AND model = 'm' AND month = ?", month).First(&usage).Error)
	assert.Equal(t, net, usage.ConsumedQuota)
}

// 未达档不亏：规则只有高档时，低累计原价（不存在「按更高折扣提前享受」）。
func TestBelowThresholdOriginalPrice(t *testing.T) {
	setupTierTestDB(t)
	mustRule(t, 8, 1, "m", 100, 0.7)
	assert.Equal(t, 1.0, GetCurrentDiscount(8, 1, "m"))
	require.NoError(t, model.AdjustTierUsage(8, 1, "m", 99))
	assert.Equal(t, 1.0, GetCurrentDiscount(8, 1, "m"))
	require.NoError(t, model.AdjustTierUsage(8, 1, "m", 1))
	assert.Equal(t, 0.7, GetCurrentDiscount(8, 1, "m"))
}

// —— helpers ——

func monthStartForTest(month string) (int64, int64) {
	// 与 model.tierMonthRange 同口径（不导出，测试内复刻）。
	loc := time.FixedZone("CST", 8*3600)
	start, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		panic(err)
	}
	return start.Unix(), start.AddDate(0, 1, 0).Unix()
}
