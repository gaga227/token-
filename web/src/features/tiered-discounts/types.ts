/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/

export interface TierRule {
  id: number
  user_id: number
  channel_id: number
  model: string
  /** 累计实耗阈值（quota） */
  threshold_quota: number
  /** 折扣（0.92 = 92 折，1 = 原价） */
  discount: number
  enabled: boolean
  remark: string
  created_time: number
  updated_time: number
  /** 展示用：阈值换算成元（后端按充值价换算） */
  threshold_rmb: number
}

export interface TierRuleInput {
  user_id: number
  channel_id: number
  model: string
  /** 累计达到（元） */
  threshold_rmb: number
  /** 折扣（1 = 原价，0.75 = 七五折） */
  discount: number
  enabled?: boolean
  remark?: string
}

export interface TierUsageMonthly {
  id: number
  user_id: number
  channel_id: number
  model: string
  month: string
  /** 当月净实耗（quota，消费−退款） */
  consumed_quota: number
  created_time: number
  updated_time: number
  /** 展示用：换算成元 */
  consumed_rmb: number
}

/** 下拉用：精简用户 */
export interface SimpleUser {
  id: number
  username: string
  display_name?: string
}

/** 下拉用：精简渠道（models 为逗号分隔的模型名） */
export interface SimpleChannel {
  id: number
  name: string
  models: string
  status?: number
}

/** 批量保存结果 */
export interface BatchSaveResult {
  created: number
  skipped: number
  failed: string[]
}

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}
