/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/

/** 报表明细行（后端 ReportDetailRow，内嵌 Log 字段） */
export interface ReportDetailRow {
  id: number
  created_at: number
  type: number // 2 消耗 / 6 退还 / 5 失败
  request_id?: string
  user_id: number
  username: string
  channel: number
  channel_name: string
  model_name: string
  token_name: string
  group: string
  prompt_tokens: number
  completion_tokens: number
  use_time: number
  /** 实付（净额，退还为负） */
  paid_quota: number
  /** 原价（无折扣行 = 实付绝对值） */
  origin_quota: number
  /** 折扣率，0 = 无（历史数据） */
  discount: number
}

/** 明细合计行（当前筛选全量） */
export interface ReportTotals {
  requests: number
  refunds: number
  prompt_tokens: number
  completion_tokens: number
  paid_quota: number
  origin_quota: number
  discount_quota: number
  /** 阶梯实付净额（满足档位行的 原价×折扣） */
  tier_paid_quota: number
}

/** 汇总行 */
export interface ReportSummaryRow {
  user_id: number
  username: string
  channel_id: number
  channel_name: string
  model: string
  requests: number
  refunds: number
  prompt_tokens: number
  completion_tokens: number
  paid_quota: number
  origin_quota: number
  discount_quota: number
}

/** 汇总维度：user | user_channel | user_channel_model */
export type SummaryDim = 'user' | 'user_channel' | 'user_channel_model'

export interface ReportDetailsData {
  page: number
  page_size: number
  total: number
  items: ReportDetailRow[]
  totals: ReportTotals
  rmb_rate: number
}

export interface ReportSummaryData {
  dim: SummaryDim
  items: ReportSummaryRow[]
  rmb_rate: number
}
