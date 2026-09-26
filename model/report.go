package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
)

// TierRmbRate quota → 元 的换算率（= Price / QuotaPerUnit），供前端展示金额。
func TierRmbRate() float64 {
	return operation_setting.Price / common.QuotaPerUnit
}

// ============ 数据报表（运营对账，2026-09-24 定稿） ============
// 口径（净额）：消耗(type=2)记正、退还(type=6)记负；错误日志(type=5)金额为 0，
// 仅在明细里展示状态。原价/折扣取自 logs.other 的 tier_origin_quota / tier_discount
// （配阶梯折扣之后的数据才有这两个字段，历史数据原价=实付、无优惠）。

// reportSignExpr 净额符号：退还记负。
const reportSignExpr = "CASE WHEN logs.type = 6 THEN -1 ELSE 1 END"

// reportOriginExpr 从 other JSON 提取 tier_origin_quota（缺失/非法/空串时为 0）。
// 历史日志 other 可能是空串或非 JSON 文本，必须先做合法性守卫，否则
// SQLite json_extract / PG ::jsonb 都会直接报 malformed JSON。
func reportOriginExpr() string {
	switch {
	case common.UsingLogDatabase(common.DatabaseTypeSQLite):
		return "CASE WHEN json_valid(logs.other) THEN COALESCE(json_extract(logs.other, '$.tier_origin_quota'), 0) ELSE 0 END"
	case common.UsingLogDatabase(common.DatabaseTypeMySQL):
		return "COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(logs.other, '$.tier_origin_quota')) AS DECIMAL(24,2)), 0)"
	case common.UsingLogDatabase(common.DatabaseTypePostgreSQL):
		// 空串/非 JSON 文本守卫 + jsonb 兜底
		return "COALESCE(CAST(CASE WHEN logs.other ~ '^\\s*\\{' THEN NULLIF(logs.other, '')::jsonb->>'tier_origin_quota' END AS NUMERIC), 0)"
	case common.UsingLogDatabase(common.DatabaseTypeClickHouse):
		return "JSONExtractFloat(logs.other, 'tier_origin_quota')"
	}
	return "0"
}

// reportDiscountExpr 优惠合计：仅对带 tier 字段的行按净额口径计 (原价-实付)，
// 历史无折扣行不计负数。
func reportDiscountExpr() string {
	origin := reportOriginExpr()
	return fmt.Sprintf(
		"SUM(CASE WHEN %s > 0 THEN %s * (%s - logs.quota) ELSE 0 END)",
		origin, reportSignExpr, origin)
}

// reportTierDiscountExpr 从 other JSON 提取 tier_discount（折扣率，缺失/非法/空串时为 0）。
// 守卫方式与 reportOriginExpr 一致。
func reportTierDiscountExpr() string {
	switch {
	case common.UsingLogDatabase(common.DatabaseTypeSQLite):
		return "CASE WHEN json_valid(logs.other) THEN COALESCE(json_extract(logs.other, '$.tier_discount'), 0) ELSE 0 END"
	case common.UsingLogDatabase(common.DatabaseTypeMySQL):
		return "COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(logs.other, '$.tier_discount')) AS DECIMAL(10,6)), 0)"
	case common.UsingLogDatabase(common.DatabaseTypePostgreSQL):
		// 空串/非 JSON 文本守卫 + jsonb 兜底
		return "COALESCE(CAST(CASE WHEN logs.other ~ '^\\s*\\{' THEN NULLIF(logs.other, '')::jsonb->>'tier_discount' END AS NUMERIC), 0)"
	case common.UsingLogDatabase(common.DatabaseTypeClickHouse):
		return "JSONExtractFloat(logs.other, 'tier_discount')"
	}
	return "0"
}

// ReportFilter 报表查询条件（多维多选）。
type ReportFilter struct {
	UserIds    []int
	ChannelIds []int
	Models     []string
	Group      string
	TokenName  string
	Types      []int // 默认 [2,6]；含 5 则明细带出失败请求
	StartAt    int64
	EndAt      int64
}

func (f *ReportFilter) apply(tx *gorm.DB) *gorm.DB {
	if len(f.UserIds) > 0 {
		tx = tx.Where("logs.user_id IN ?", f.UserIds)
	}
	if len(f.ChannelIds) > 0 {
		tx = tx.Where("logs.channel_id IN ?", f.ChannelIds)
	}
	if len(f.Models) > 0 {
		tx = tx.Where("logs.model_name IN ?", f.Models)
	}
	if f.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", f.Group)
	}
	if f.TokenName != "" {
		tx = tx.Where("logs.token_name = ?", f.TokenName)
	}
	if len(f.Types) > 0 {
		tx = tx.Where("logs.type IN ?", f.Types)
	} else {
		tx = tx.Where("logs.type IN ?", []int{LogTypeConsume, LogTypeRefund})
	}
	if f.StartAt != 0 {
		tx = tx.Where("logs.created_at >= ?", f.StartAt)
	}
	if f.EndAt != 0 {
		tx = tx.Where("logs.created_at <= ?", f.EndAt)
	}
	return tx
}

// ReportDetailRow 明细行：内嵌日志字段 + 折扣三件套（净额口径）。
type ReportDetailRow struct {
	Log
	PaidQuota   int64   `json:"paid_quota"`   // 实付（退还为负）
	OriginQuota int64   `json:"origin_quota"` // 原价（无折扣行 = 实付绝对值）
	Discount    float64 `json:"discount"`     // 折扣率，0 = 无（历史数据）
}

// reportBuildRows 把日志行转成明细行（净额口径：退还记负 + 折扣三件套）。
func reportBuildRows(logs []*Log) []ReportDetailRow {
	rows := make([]ReportDetailRow, 0, len(logs))
	for _, l := range logs {
		row := ReportDetailRow{Log: *l}
		paid := int64(l.Quota)
		if l.Type == LogTypeRefund {
			paid = -paid
		}
		row.PaidQuota = paid
		row.OriginQuota = paid // 默认原价=实付
		if l.Other != "" {
			var other map[string]interface{}
			if json.Unmarshal([]byte(l.Other), &other) == nil {
				if v, ok := other["tier_origin_quota"].(float64); ok && v > 0 {
					origin := int64(math.Round(v))
					if l.Type == LogTypeRefund {
						origin = -origin
					}
					row.OriginQuota = origin
				}
				if v, ok := other["tier_discount"].(float64); ok && v > 0 {
					row.Discount = v
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// reportFillChannelNames 批量补齐渠道名，失败不阻塞报表。
func reportFillChannelNames(rows []ReportDetailRow) {
	ids := make([]int, 0, len(rows))
	seen := map[int]bool{}
	for _, r := range rows {
		if r.ChannelId != 0 && !seen[r.ChannelId] {
			seen[r.ChannelId] = true
			ids = append(ids, r.ChannelId)
		}
	}
	if len(ids) == 0 {
		return
	}
	type ch struct {
		Id   int
		Name string
	}
	var chs []ch
	if err := DB.Table("channels").Select("id, name").Where("id IN ?", ids).Scan(&chs).Error; err != nil {
		return
	}
	m := map[int]string{}
	for _, c := range chs {
		m[c.Id] = c.Name
	}
	for i := range rows {
		if rows[i].ChannelId != 0 {
			rows[i].ChannelName = m[rows[i].ChannelId]
		}
	}
}

// reportLogOrder 明细排序（ClickHouse 语法不同需单独处理）。
func reportLogOrder() string {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return clickHouseLogOrder("logs.")
	}
	return "logs.created_at desc, logs.id desc"
}

// GetReportDetails 分页明细，时间倒序。
func GetReportDetails(f ReportFilter, startIdx int, num int) (rows []ReportDetailRow, total int64, err error) {
	tx := f.apply(LOG_DB.Model(&Log{}))
	if err = tx.Count(&total).Error; err != nil {
		return
	}
	var logs []*Log
	if err = tx.Order(reportLogOrder()).Limit(num).Offset(startIdx).Find(&logs).Error; err != nil {
		return
	}
	rows = reportBuildRows(logs)
	reportFillChannelNames(rows)
	return
}

// GetReportDetailsBatch 游标分页（keyset）明细批，供 CSV 全量导出用。
// 按 (created_at, id) 严格递减翻页，避免 OFFSET 深分页的平方级扫描放大；
// beforeCreatedAt/beforeId 传 0 表示从头开始。
func GetReportDetailsBatch(f ReportFilter, beforeCreatedAt int64, beforeId int, num int) (rows []ReportDetailRow, err error) {
	tx := f.apply(LOG_DB.Model(&Log{}))
	if beforeCreatedAt > 0 {
		tx = tx.Where(
			"(logs.created_at < ?) OR (logs.created_at = ? AND logs.id < ?)",
			beforeCreatedAt, beforeCreatedAt, beforeId,
		)
	}
	var logs []*Log
	if err = tx.Order(reportLogOrder()).Limit(num).Find(&logs).Error; err != nil {
		return
	}
	rows = reportBuildRows(logs)
	reportFillChannelNames(rows)
	return
}

// ReportTotals 合计行（当前筛选全量，非当前页）。
type ReportTotals struct {
	Requests         int64 `json:"requests"`          // 消耗笔数
	Refunds          int64 `json:"refunds"`           // 退还笔数
	PromptTokens     int64 `json:"prompt_tokens"`     // 输入 tokens（消耗行）
	CompletionTokens int64 `json:"completion_tokens"` // 输出 tokens（消耗行）
	PaidQuota        int64 `json:"paid_quota"`        // 实付净额（quota）
	OriginQuota      int64 `json:"origin_quota"`      // 原价净额（quota，无折扣行=实付）
	DiscountQuota    int64 `json:"discount_quota"`    // 优惠金额（quota，仅折扣行）
	TierPaidQuota    int64 `json:"tier_paid_quota"`   // 阶梯实付净额（quota，满足档位行的 原价×折扣）
}

// GetReportTotals 当前筛选的合计（SQL 聚合，一次查完）。
func GetReportTotals(f ReportFilter) (t ReportTotals, err error) {
	origin := reportOriginExpr()
	originNet := fmt.Sprintf("%s * %s", reportSignExpr, origin)
	selects := []string{
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN 1 ELSE 0 END), 0) as requests",
		"COALESCE(SUM(CASE WHEN logs.type = 6 THEN 1 ELSE 0 END), 0) as refunds",
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN logs.prompt_tokens ELSE 0 END), 0) as prompt_tokens",
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN logs.completion_tokens ELSE 0 END), 0) as completion_tokens",
		fmt.Sprintf("COALESCE(SUM(%s * logs.quota), 0) as paid_quota", reportSignExpr),
		fmt.Sprintf("COALESCE(SUM(CASE WHEN %s > 0 THEN %s ELSE %s * logs.quota END), 0) as origin_quota", origin, originNet, reportSignExpr),
		reportDiscountExpr() + " as discount_quota",
		fmt.Sprintf("COALESCE(SUM(CASE WHEN %s > 0 THEN %s * %s * %s ELSE 0 END), 0) as tier_paid_quota", origin, reportSignExpr, origin, reportTierDiscountExpr()),
	}
	err = f.apply(LOG_DB.Model(&Log{})).
		Select(strings.Join(selects, ", ")).
		Scan(&t).Error
	return
}

// ReportSummaryRow 汇总行。
type ReportSummaryRow struct {
	UserId           int    `json:"user_id"`
	Username         string `json:"username"`
	ChannelId        int    `json:"channel_id"`
	ChannelName      string `json:"channel_name"`
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	Refunds          int64  `json:"refunds"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	PaidQuota        int64  `json:"paid_quota"`
	OriginQuota      int64  `json:"origin_quota"`
	DiscountQuota    int64  `json:"discount_quota"`
}

// GetReportSummary 分组聚合。dim: user | user_channel | user_channel_model。
// 汇总恒为净额口径（type IN (2,6)），错误日志金额为 0 不影响，也不计入请求数。
func GetReportSummary(f ReportFilter, dim string) (rows []ReportSummaryRow, err error) {
	// 汇总口径固定消耗+退还
	f.Types = []int{LogTypeConsume, LogTypeRefund}

	groupCols := []string{"logs.user_id", "logs.username"}
	selectCols := []string{
		"logs.user_id as user_id", "logs.username as username",
		"0 as channel_id", "'' as channel_name", "'' as model",
	}
	if dim == "user_channel" || dim == "user_channel_model" {
		groupCols = append(groupCols, "logs.channel_id")
		selectCols[2] = "logs.channel_id as channel_id"
	}
	if dim == "user_channel_model" {
		groupCols = append(groupCols, "logs.model_name")
		selectCols[4] = "logs.model_name as model"
	}
	origin := reportOriginExpr()
	originNet := fmt.Sprintf("%s * %s", reportSignExpr, origin)
	selectCols = append(selectCols,
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN 1 ELSE 0 END), 0) as requests",
		"COALESCE(SUM(CASE WHEN logs.type = 6 THEN 1 ELSE 0 END), 0) as refunds",
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN logs.prompt_tokens ELSE 0 END), 0) as prompt_tokens",
		"COALESCE(SUM(CASE WHEN logs.type = 2 THEN logs.completion_tokens ELSE 0 END), 0) as completion_tokens",
		fmt.Sprintf("COALESCE(SUM(%s * logs.quota), 0) as paid_quota", reportSignExpr),
		fmt.Sprintf("COALESCE(SUM(CASE WHEN %s > 0 THEN %s ELSE %s * logs.quota END), 0) as origin_quota", origin, originNet, reportSignExpr),
		reportDiscountExpr() + " as discount_quota",
	)

	tx := f.apply(LOG_DB.Table("logs")).
		Select(strings.Join(selectCols, ", ")).
		Group(strings.Join(groupCols, ", ")).
		Order("paid_quota desc")
	if err = tx.Scan(&rows).Error; err != nil {
		return
	}
	// 渠道名批量补齐
	ids := make([]int, 0, len(rows))
	seen := map[int]bool{}
	for _, r := range rows {
		if r.ChannelId != 0 && !seen[r.ChannelId] {
			seen[r.ChannelId] = true
			ids = append(ids, r.ChannelId)
		}
	}
	if len(ids) > 0 {
		type ch struct {
			Id   int
			Name string
		}
		var chs []ch
		if err = DB.Table("channels").Select("id, name").Where("id IN ?", ids).Scan(&chs).Error; err == nil {
			m := map[int]string{}
			for _, c := range chs {
				m[c.Id] = c.Name
			}
			for i := range rows {
				if rows[i].ChannelId != 0 {
					rows[i].ChannelName = m[rows[i].ChannelId]
				}
			}
		} else {
			err = nil
		}
	}
	return
}
