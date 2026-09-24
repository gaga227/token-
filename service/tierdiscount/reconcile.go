package tierdiscount

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// reconcileInterval 对账周期：每小时从日志重算修正汇总表（只修数、不发钱）。
// 实时性不依赖它——累计在日志写入出口实时维护，这里只兜底极端情况
// （如扣费成功但汇总累加失败）。
const reconcileInterval = time.Hour

// Start 启动阶梯折扣后台任务：
//  1. 启动时回填当月累计（对每个已配置规则的身份，从日志重算一次，保证
//     上线瞬间跨档判断正确）；
//  2. 每小时对账一次（修正实时累加可能的漂移）。
func Start() {
	go func() {
		common.SysLog("[tier-discount] 回填当月累计开始")
		n, err := model.BackfillTierUsageCurrentMonth()
		if err != nil {
			common.SysError("[tier-discount] 回填当月累计失败: " + err.Error())
		} else {
			common.SysLog("[tier-discount] 回填当月累计完成，覆盖规则身份 " + fmt.Sprintf("%d", n) + " 个")
		}
	}()
	go reconcileLoop()
}

func reconcileLoop() {
	ctx := context.Background()
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for range ticker.C {
		reconcileOnce(ctx)
	}
}

// reconcileOnce 从日志重算所有规则身份的当月汇总；与实时值有偏差时修正并告警。
func reconcileOnce(ctx context.Context) {
	scopes, err := model.TierRuleScopeList()
	if err != nil {
		common.SysError("[tier-discount] 对账读取规则身份失败: " + err.Error())
		return
	}
	month := model.TierDiscountMonth()
	for _, s := range scopes {
		var current int64
		var usage model.TierUsageMonthly
		if err := model.DB.Where("user_id = ? AND channel_id = ? AND model = ? AND month = ?",
			s.UserId, s.ChannelId, s.Model, month).First(&usage).Error; err == nil {
			current = usage.ConsumedQuota
		}
		net, err := model.RecalcTierUsageScope(s.UserId, s.ChannelId, s.Model, month)
		if err != nil {
			common.SysError("[tier-discount] 对账重算失败 user=" + fmt.Sprintf("%d", s.UserId) +
				" channel=" + fmt.Sprintf("%d", s.ChannelId) + " model=" + s.Model + ": " + err.Error())
			continue
		}
		if net != current {
			common.SysLog("[tier-discount] 对账修正 user=" + fmt.Sprintf("%d", s.UserId) +
				" channel=" + fmt.Sprintf("%d", s.ChannelId) + " model=" + s.Model +
				" " + fmt.Sprintf("%d", current) + " -> " + fmt.Sprintf("%d", net))
		}
	}
}
