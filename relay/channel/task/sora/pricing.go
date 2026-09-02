package sora

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relay/helper"
)

// ============================
// MiniMax 系列（maitoken）视频计费
// 基准：768P 每秒 0.5 元（对应 ModelPrice 0.0737463 美元/秒 = 0.5 / 6.78，后台配置汇率）
// 计费将输出秒数、分辨率、图片超额、视频输入统一折算为「等效基准秒数」，
// 作为 OtherRatio "seconds" 叠加（OtherRatios 为纯乘法，素材费折算成秒数而非相乘）。
// 官网价：768P=0.50 元/秒、2K=0.80 元/秒、Max-480P=0.33、Max-768P=0.50；
// 图片 ≤5 张免费，超出 0.20 元/张；视频输入按实际输入时长 × 输出分辨率单价
// （时长从上传的 MP4/MOV 文件解析；解析失败 fallback 近似为输出秒数）。
// ============================

const (
	// MiniMaxBaseModelPrice 768P 基准价的 ModelPrice（美元/秒）：0.5 元 ÷ 后台汇率 6.78
	// 通过 init() 注册到任务模型价格表，不再写入全局 model_ratio 默认价格表。
	MiniMaxBaseModelPrice = 0.0737463
	// MiniMaxFreeImageCount 免费图片张数
	MiniMaxFreeImageCount = 5
	// MiniMaxExtraImageRatio 超出图片每张折算的等效基准秒数（0.2 元 / 0.5 元/秒）
	MiniMaxExtraImageRatio = 0.4
	// MiniMaxVideoFallbackSecondsRatio 视频时长解析失败时的近似倍率：
	// 按「输入时长 = 输出秒数」处理，费用 = 输出费用 × 2（保持解析能力上线前的行为）。
	MiniMaxVideoFallbackSecondsRatio = 2.0
)

// init 将 MiniMax 系列的基础价格注册到任务模型价格注册表。
// 查询优先级：后台「模型定价」配置 > 本注册表 > 全局默认价格表 > 倍率路径。
// 后台如需覆盖默认价，直接在「模型定价」中配置即可。
func init() {
	for _, name := range []string{
		"MiniMax-H3",
		"minimax-h3-base",
		"minimax-h3-base-fast",
		"minimax-h3-mini",
		// MiniMax-H3-Max 预留（上游开放后启用，届时补注册）
	} {
		helper.RegisterTaskModelPrice(name, MiniMaxBaseModelPrice)
	}
}

// miniMaxResolutionRatio 模型 → 输出分辨率档 → 相对 768P 基准的倍率
var miniMaxResolutionRatio = map[string]map[string]float64{
	"MiniMax-H3": {
		"768p": 1.0,
		"2k":   1.6, // 0.80 / 0.50
	},
	"minimax-h3-base": {
		"768p": 1.0,
		"2k":   1.6,
	},
	"minimax-h3-base-fast": {
		"768p": 1.0,
		"2k":   1.6,
	},
	"minimax-h3-mini": {
		"768p": 1.0,
		"2k":   1.6,
	},
	// MiniMax-H3-Max 预留（上游开放后启用）：
	// "MiniMax-H3-Max": {
	// 	"480p": 0.66, // 0.33 / 0.50
	// 	"768p": 1.0,
	// },
}

// GetMiniMaxResolutionRatio 返回模型在指定 size 下的分辨率倍率；未知模型/尺寸按 768P 基准 1.0。
func GetMiniMaxResolutionRatio(modelName, size string) float64 {
	prices, ok := miniMaxResolutionRatio[modelName]
	if !ok {
		return 1.0
	}
	ratio, ok := prices[miniMaxResolutionTier(size)]
	if !ok {
		return 1.0
	}
	return ratio
}

// miniMaxResolutionTier 根据输出分辨率长边判断档位：≥1792 → 2k，≤854 → 480p，其余 768p。
func miniMaxResolutionTier(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return "768p"
	}
	parts := strings.SplitN(size, "x", 2)
	if len(parts) != 2 {
		return "768p"
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return "768p"
	}
	long := w
	if h > long {
		long = h
	}
	switch {
	case long >= 1792:
		return "2k"
	case long <= 854:
		return "480p"
	default:
		return "768p"
	}
}
