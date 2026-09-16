/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/

export interface TierDiscountRule {
  id: number
  user_id: number
  group_name: string
  model: string
  threshold_cents: number
  discount: number
  buffer_ratio: number
  enabled: boolean
  created_time: number
  updated_time: number
}

export interface TierDiscountRuleInput {
  user_id: number
  group_name: string
  model: string
  /** 累计达到（元） */
  threshold_rmb: number
  /** 折扣（1 = 原价，0.75 = 七五折） */
  discount: number
  /** 滞后缓冲比例 [0,1] */
  buffer_ratio: number
  enabled: boolean
}

export interface TierDiscountProgress {
  id: number
  user_id: number
  group_name: string
  model: string
  month: string
  total_cents: number
  cash_cents: number
  current_threshold_cents: number
  current_discount: number
  last_rebated_threshold_cents: number
  updated_time: number
}

export interface TierDiscountRebate {
  id: number
  user_id: number
  group_name: string
  model: string
  month: string
  from_threshold_cents: number
  to_threshold_cents: number
  base_cents: number
  old_discount: number
  new_discount: number
  rebate_cents: number
  rebate_quota: number
  status: string
  created_time: number
}

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}
