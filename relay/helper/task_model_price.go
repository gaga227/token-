package helper

import "sync"

// ============================
// 任务类模型价格注册表
// 供各任务适配器（sora / gemini / doubao 等）以独立文件维护自己的模型价格，
// 而不必写入全局 model_ratio 默认价格表。
// 查询优先级：后台「模型定价」配置 → 本注册表 → 全局默认价格表 → 倍率路径。
// ============================

var (
	taskModelPriceMu        sync.RWMutex
	taskModelPriceOverrides = map[string]float64{}
)

// RegisterTaskModelPrice 注册任务类模型的基础价格（美元/秒 或 美元/次，按适配器计费口径）。
// 建议在各适配器包的 init() 中调用。
func RegisterTaskModelPrice(model string, price float64) {
	taskModelPriceMu.Lock()
	defer taskModelPriceMu.Unlock()
	taskModelPriceOverrides[model] = price
}

// GetTaskModelPrice 查询任务类模型注册价格。
func GetTaskModelPrice(model string) (float64, bool) {
	taskModelPriceMu.RLock()
	defer taskModelPriceMu.RUnlock()
	price, ok := taskModelPriceOverrides[model]
	return price, ok
}
