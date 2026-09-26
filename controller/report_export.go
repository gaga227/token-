package controller

import (
	"bufio"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ============ 异步限速导出（2026-09-27 定稿） ============
// 目标：导出完全不影响线上业务——不在 HTTP 请求线程里拉库，
// 后台单 worker 按固定节奏慢速跑（批 1000 行 + 批间休眠），
// CSV 落盘后由用户取件。查询走 LOG_DB，将来读写分离指到只读副本即自动隔离。
// 任务为内存态，进程重启即丢（对账导出重跑即可），不落库。

const (
	reportExportBatch     = 1000                            // 每批行数（游标一批）
	reportExportSleep     = 200 * time.Millisecond          // 批间休眠：每秒至多 1 次范围查询
	reportExportDir       = "data/exports"                  // 导出文件目录
	reportExportTTL       = 24 * time.Hour                  // 文件与任务保留时长
	reportExportQueueCap  = 20                              // 排队上限
	reportExportFilename  = "report_%s_%s.csv"              // type_timestamp
)

type exportTask struct {
	Id        string             `json:"id"`
	Type      string             `json:"type"` // details | summary
	Dim       string             `json:"dim,omitempty"`
	Status    string             `json:"status"` // pending | running | done | failed
	Rows      int64              `json:"rows"`   // 已导出行数（details 为进度，summary 为分组数）
	Error     string             `json:"error,omitempty"`
	Filename  string             `json:"filename,omitempty"`
	FilePath  string             `json:"-"`
	Filter    model.ReportFilter `json:"-"`
	FilterDesc string            `json:"filter_desc,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	DoneAt    time.Time          `json:"done_at,omitempty"`
}

var (
	reportExportMu    sync.Mutex
	reportExportTasks = map[string]*exportTask{}
	reportExportKeys  = map[string]string{} // 去重键（type+dim+筛选）→ 进行中任务 id
	reportExportQueue chan *exportTask
	reportExportOnce  sync.Once
)

// InitReportExportWorker 启动导出 worker 与清理协程（main 调用一次）。
func InitReportExportWorker() {
	reportExportOnce.Do(func() {
		if err := os.MkdirAll(reportExportDir, 0o755); err != nil {
			common.SysLog("report export: mkdir failed: " + err.Error())
		}
		reportExportCleanup()
		go reportExportCleanupLoop()
		reportExportQueue = make(chan *exportTask, reportExportQueueCap)
		go reportExportWorker()
	})
}

func reportExportWorker() {
	for t := range reportExportQueue {
		reportExportSetStatus(t, "running")
		var err error
		if t.Type == "summary" {
			err = reportExportRunSummary(t)
		} else {
			err = reportExportRunDetails(t)
		}
		reportExportMu.Lock()
		if err != nil {
			t.Status = "failed"
			t.Error = err.Error()
			common.SysLog("report export failed: " + t.Id + " " + err.Error())
		} else {
			t.Status = "done"
			t.DoneAt = time.Now()
			common.SysLog(fmt.Sprintf("report export done: %s rows=%d file=%s", t.Id, t.Rows, t.Filename))
		}
		reportExportMu.Unlock()
	}
}

func reportExportSetStatus(t *exportTask, s string) {
	reportExportMu.Lock()
	t.Status = s
	reportExportMu.Unlock()
}

func reportExportSetRows(t *exportTask, rows int64) {
	reportExportMu.Lock()
	t.Rows = rows
	reportExportMu.Unlock()
}

// reportExportKey 去重键：同类型同筛选进行中的任务复用。
func reportExportKey(t *exportTask) string {
	return fmt.Sprintf("%s|%s|%v|%v|%v|%s|%s|%d|%d",
		t.Type, t.Dim, t.Filter.UserIds, t.Filter.ChannelIds, t.Filter.Models,
		t.Filter.Group, t.Filter.TokenName, t.Filter.StartAt, t.Filter.EndAt)
}

// SubmitReportExport 提交导出任务（POST，筛选参数与查询接口同名）。
func SubmitReportExport(c *gin.Context) {
	t := &exportTask{
		Type:      c.Query("type"),
		Dim:       c.DefaultQuery("dim", "user_channel_model"),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	if t.Type != "details" && t.Type != "summary" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "type 必须是 details 或 summary"})
		return
	}
	if t.Dim != "user" && t.Dim != "user_channel" && t.Dim != "user_channel_model" {
		t.Dim = "user_channel_model"
	}
	f, err := reportBuildFilter(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	t.Filter = f
	t.FilterDesc = fmt.Sprintf("用户=%v 渠道=%v 模型=%v 范围=%s~%s",
		f.UserIds, f.ChannelIds, f.Models, time.Unix(f.StartAt, 0).In(reportCST).Format("2006-01-02"), time.Unix(f.EndAt, 0).In(reportCST).Format("2006-01-02"))

	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	t.Id = hex.EncodeToString(buf)

	reportExportMu.Lock()
	if key, ok := reportExportKeys[reportExportKey(t)]; ok {
		reportExportMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": key, "reused": true}})
		return
	}
	if len(reportExportQueue) >= reportExportQueueCap {
		reportExportMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "导出排队已满，请稍后再试"})
		return
	}
	reportExportTasks[t.Id] = t
	reportExportKeys[reportExportKey(t)] = t.Id
	reportExportMu.Unlock()

	reportExportQueue <- t
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": t.Id, "status": t.Status}})
}

// GetReportExportStatus 进度查询：?id= 单个；否则返回最近任务列表。
func GetReportExportStatus(c *gin.Context) {
	if id := c.Query("id"); id != "" {
		reportExportMu.Lock()
		t, ok := reportExportTasks[id]
		reportExportMu.Unlock()
		if !ok {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "任务不存在（可能已过 24h 清理或服务重启）"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": t})
		return
	}
	reportExportMu.Lock()
	list := make([]*exportTask, 0, len(reportExportTasks))
	for _, t := range reportExportTasks {
		list = append(list, t)
	}
	reportExportMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	if len(list) > 50 {
		list = list[:50]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetReportExportDownload 取件：任务完成后下载 CSV 文件。
func GetReportExportDownload(c *gin.Context) {
	id := c.Query("id")
	reportExportMu.Lock()
	t, ok := reportExportTasks[id]
	reportExportMu.Unlock()
	if !ok {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "任务不存在"})
		return
	}
	if t.Status != "done" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "任务尚未完成: " + t.Status})
		return
	}
	if _, err := os.Stat(t.FilePath); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "文件已过期清理，请重新导出"})
		return
	}
	c.FileAttachment(t.FilePath, t.Filename)
}

// ============ 任务执行 ============

func reportExportFilePath(t *exportTask) string {
	return filepath.Join(reportExportDir, fmt.Sprintf(reportExportFilename, t.Type, time.Now().In(reportCST).Format("20060102_150405")))
}

// reportExportRunDetails 明细导出：游标分批 + 批间休眠，匀速拉库。
func reportExportRunDetails(t *exportTask) error {
	path := reportExportFilePath(t)
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	bw := bufio.NewWriter(fh)
	defer bw.Flush()
	w := csv.NewWriter(bw)

	// BOM：Excel 直开不乱码
	if _, err := bw.WriteString("\xEF\xBB\xBF"); err != nil {
		return err
	}
	header := []string{"时间", "类型", "请求ID", "用户ID", "用户名", "渠道ID", "渠道名", "模型", "令牌", "分组",
		"输入tokens", "输出tokens", "原价(元)", "折扣", "实付(元)", "阶梯折扣", "阶梯实付(元)", "优惠(元)", "实付(quota)", "耗时(秒)"}
	if err := w.Write(header); err != nil {
		return err
	}
	rate := model.TierRmbRate()
	var curCreatedAt int64
	var curId int
	var rows int64
	for {
		batch, err := model.GetReportDetailsBatch(t.Filter, curCreatedAt, curId, reportExportBatch)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			typeLabel := "消耗"
			if r.Type == model.LogTypeRefund {
				typeLabel = "退还"
			} else if r.Type == model.LogTypeError {
				typeLabel = "失败"
			}
			discount := ""
			if r.Discount > 0 {
				discount = strconv.FormatFloat(r.Discount, 'f', -1, 64)
			}
			tierPaid := "" // 阶梯实付 = 原价 × 档位折扣
			if r.Discount > 0 {
				tierPaid = fmt.Sprintf("%.4f", math.Round(float64(r.OriginQuota)*r.Discount)*rate)
			}
			if err := w.Write([]string{
				reportCsvTime(r.CreatedAt),
				typeLabel,
				r.RequestId,
				strconv.Itoa(r.UserId),
				r.Username,
				strconv.Itoa(r.ChannelId),
				r.ChannelName,
				r.ModelName,
				r.TokenName,
				r.Group,
				strconv.Itoa(r.PromptTokens),
				strconv.Itoa(r.CompletionTokens),
				fmt.Sprintf("%.4f", float64(r.OriginQuota)*rate),
				discount,
				fmt.Sprintf("%.4f", float64(r.PaidQuota)*rate),
				discount,
				tierPaid,
				fmt.Sprintf("%.4f", float64(r.OriginQuota-r.PaidQuota)*rate),
				strconv.FormatInt(r.PaidQuota, 10),
				strconv.Itoa(r.UseTime),
			}); err != nil {
				return err
			}
		}
		last := batch[len(batch)-1]
		curCreatedAt = last.CreatedAt
		curId = last.Id
		rows += int64(len(batch))
		reportExportSetRows(t, rows)
		w.Flush()
		if err := w.Error(); err != nil {
			return err
		}
		if len(batch) < reportExportBatch {
			break
		}
		time.Sleep(reportExportSleep) // 限速：给业务让路
	}
	t.Filename = filepath.Base(path)
	t.FilePath = path
	return nil
}

// reportExportRunSummary 汇总导出：单次分组聚合后落盘。
func reportExportRunSummary(t *exportTask) error {
	rows, err := model.GetReportSummary(t.Filter, t.Dim)
	if err != nil {
		return err
	}
	path := reportExportFilePath(t)
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	bw := bufio.NewWriter(fh)
	defer bw.Flush()
	w := csv.NewWriter(bw)
	if _, err := bw.WriteString("\xEF\xBB\xBF"); err != nil {
		return err
	}
	header := []string{"用户ID", "用户名", "渠道ID", "渠道名", "模型", "请求数", "退还笔数",
		"输入tokens", "输出tokens", "原价(元)", "优惠(元)", "实付(元)", "实付(quota)"}
	if err := w.Write(header); err != nil {
		return err
	}
	rate := model.TierRmbRate()
	for _, r := range rows {
		if err := w.Write([]string{
			strconv.Itoa(r.UserId),
			r.Username,
			strconv.Itoa(r.ChannelId),
			r.ChannelName,
			r.Model,
			strconv.FormatInt(r.Requests, 10),
			strconv.FormatInt(r.Refunds, 10),
			strconv.FormatInt(r.PromptTokens, 10),
			strconv.FormatInt(r.CompletionTokens, 10),
			fmt.Sprintf("%.4f", float64(r.OriginQuota)*rate),
			fmt.Sprintf("%.4f", float64(r.DiscountQuota)*rate),
			fmt.Sprintf("%.4f", float64(r.PaidQuota)*rate),
			strconv.FormatInt(r.PaidQuota, 10),
		}); err != nil {
			return err
		}
	}
	t.Filename = filepath.Base(path)
	t.FilePath = path
	t.Rows = int64(len(rows))
	return w.Error()
}

// ============ 清理 ============

// reportExportCleanup 删除过期文件与任务。
func reportExportCleanup() {
	entries, err := os.ReadDir(reportExportDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-reportExportTTL)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(reportExportDir, e.Name()))
		}
	}
	reportExportMu.Lock()
	for id, t := range reportExportTasks {
		ref := t.DoneAt
		if ref.IsZero() {
			ref = t.CreatedAt
		}
		if ref.Before(cutoff) {
			delete(reportExportTasks, id)
		}
	}
	// 顺带清理失联的去重键
	for k, id := range reportExportKeys {
		if _, ok := reportExportTasks[id]; !ok {
			delete(reportExportKeys, k)
		}
	}
	reportExportMu.Unlock()
}

func reportExportCleanupLoop() {
	for {
		time.Sleep(time.Hour)
		reportExportCleanup()
	}
}
