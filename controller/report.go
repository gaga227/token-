package controller

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ============ 数据报表（运营对账，2026-09-24 定稿） ============
// 两个报表共用筛选参数：用户/渠道/模型多选 + 日期范围（北京时间）。
// 口径为净额：消耗记正、退还记负；汇总恒为消耗+退还。

var reportCST = time.FixedZone("CST", 8*3600)

// reportMaxRangeDays 最长查询跨度（防全表扫），高级需求可放宽到 366。
const reportMaxRangeDays = 366

func reportParseIntList(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func reportParseStrList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// reportDayRange 把 YYYY-MM-DD（北京时间）转成时间戳区间 [start, end]。
// 兼容直接传 start_timestamp/end_timestamp。
func reportDayRange(c *gin.Context) (start int64, end int64, err error) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" && endDate == "" {
		start, _ = strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
		end, _ = strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
		return
	}
	if startDate != "" {
		t, e := time.ParseInLocation("2006-01-02", startDate, reportCST)
		if e != nil {
			err = fmt.Errorf("start_date 非法: %v", e)
			return
		}
		start = t.Unix()
	}
	if endDate != "" {
		t, e := time.ParseInLocation("2006-01-02", endDate, reportCST)
		if e != nil {
			err = fmt.Errorf("end_date 非法: %v", e)
			return
		}
		end = t.AddDate(0, 0, 1).Unix() - 1 // 当天 23:59:59
	}
	return
}

// reportBuildFilter 统一解析筛选参数。
func reportBuildFilter(c *gin.Context) (model.ReportFilter, error) {
	f := model.ReportFilter{
		UserIds:    reportParseIntList(c.Query("user_ids")),
		ChannelIds: reportParseIntList(c.Query("channel_ids")),
		Models:     reportParseStrList(c.Query("models")),
		Group:      c.Query("group"),
		TokenName:  c.Query("token_name"),
	}
	types := reportParseIntList(c.Query("types"))
	if len(types) > 0 {
		f.Types = types
	}
	start, end, err := reportDayRange(c)
	if err != nil {
		return f, err
	}
	f.StartAt, f.EndAt = start, end
	if start != 0 && end != 0 && end-start > int64(reportMaxRangeDays)*86400 {
		return f, fmt.Errorf("查询范围最长 %d 天，请缩小日期范围", reportMaxRangeDays)
	}
	return f, nil
}

// GetReportDetails 报表1：使用明细（分页 + 合计行）。
func GetReportDetails(c *gin.Context) {
	f, err := reportBuildFilter(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	pageInfo := common.GetPageQuery(c)
	rows, total, err := model.GetReportDetails(f, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	totals, err := model.GetReportTotals(f)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rows)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"page":      pageInfo.Page,
			"page_size": pageInfo.PageSize,
			"total":     pageInfo.Total,
			"items":     pageInfo.Items,
			"totals":    totals,
			"rmb_rate":  model.TierRmbRate(),
		},
	})
}

// GetReportSummary 报表2：使用汇总（维度可切换的分组聚合）。
func GetReportSummary(c *gin.Context) {
	f, err := reportBuildFilter(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	dim := c.DefaultQuery("dim", "user_channel_model")
	if dim != "user" && dim != "user_channel" && dim != "user_channel_model" {
		dim = "user_channel_model"
	}
	rows, err := model.GetReportSummary(f, dim)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"dim":      dim,
			"items":    rows,
			"rmb_rate": model.TierRmbRate(),
		},
	})
}

// reportCsvTime 北京时间字符串（CSV 用）。
func reportCsvTime(ts int64) string {
	return time.Unix(ts, 0).In(reportCST).Format("2006-01-02 15:04:05")
}

func reportCsvFilename(name string) string {
	return fmt.Sprintf("attachment; filename=%s_%s.csv", name, time.Now().In(reportCST).Format("20060102_150405"))
}

// reportCsvInit 设置 CSV 响应头并返回流式 writer。
func reportCsvInit(c *gin.Context, name string) *csv.Writer {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", reportCsvFilename(name))
	// BOM：让 Excel 正确识别 UTF-8
	c.Writer.WriteString("\xEF\xBB\xBF")
	return csv.NewWriter(c.Writer)
}

// 同步导出已移除（2026-09-27 改为异步限速任务，见 report_export.go）
