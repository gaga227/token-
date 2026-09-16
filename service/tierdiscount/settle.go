package tierdiscount

import (
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// rebateLocked 在已持有 progress 行锁的事务内执行跨档返还：
// 返还 = 当月现金累计 × (旧折 − 新折)，人民币分→quota 后直加用户余额，
// 写审计表 + 用户系统日志。调用方负责幂等判断（target 阈值 >
// LastRebatedThresholdCents 才会进来）。
func rebateLocked(tx *gorm.DB, prog *model.TierDiscountProgress, oldDiscount float64, target *model.TierDiscountRule) error {
	// 折扣差是浮点（如 1.0−0.8 = 0.1999…），四舍五入到分避免截断少钱。
	rebateCents := int64(math.Round(float64(prog.CashCents) * (oldDiscount - target.Discount)))
	if rebateCents <= 0 {
		return nil
	}
	rebateQuota := model.CentsToQuota(rebateCents)
	if rebateQuota <= 0 {
		return nil
	}

	// 余额直写（事务内），不经批量更新队列，保证与审计记录同事务原子性。
	if err := tx.Exec("UPDATE users SET quota = quota + ? WHERE id = ?", rebateQuota, prog.UserId).Error; err != nil {
		return err
	}

	rebate := model.TierDiscountRebate{
		UserId:             prog.UserId,
		GroupName:          prog.GroupName,
		Model:              prog.Model,
		Month:              prog.Month,
		FromThresholdCents: prog.LastRebatedThresholdCents,
		ToThresholdCents:   target.ThresholdCents,
		BaseCents:          prog.CashCents,
		OldDiscount:        oldDiscount,
		NewDiscount:        target.Discount,
		RebateCents:        rebateCents,
		RebateQuota:        rebateQuota,
		Status:             "success",
		CreatedTime:        time.Now().Unix(),
	}
	if err := tx.Create(&rebate).Error; err != nil {
		return err
	}

	// 用户可见日志（type=System），余额变动页面可查到返还来源。
	username := ""
	var u model.User
	if err := tx.Select("username").Where("id = ?", prog.UserId).First(&u).Error; err == nil {
		username = u.Username
	}
	content := fmt.Sprintf("阶梯折扣返还：%s（分组 %s）当月累计消费 ¥%.2f 达到 ¥%.2f 档，折扣 %.4g→%.4g，返还 ¥%.2f",
		prog.Model, prog.GroupName,
		float64(prog.TotalCents)/100, float64(target.ThresholdCents)/100,
		oldDiscount, target.Discount, float64(rebateCents)/100)
	if err := tx.Create(&model.Log{
		UserId:    prog.UserId,
		Username:  username,
		Type:      model.LogTypeSystem,
		Content:   content,
		Quota:     rebateQuota,
		ModelName: prog.Model,
		Group:     prog.GroupName,
		CreatedAt: time.Now().Unix(),
	}).Error; err != nil {
		return err
	}
	return nil
}
