/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { api } from '@/lib/api'

import type {
  ApiResponse,
  TierDiscountProgress,
  TierDiscountRebate,
  TierDiscountRule,
  TierDiscountRuleInput,
} from './types'

export async function getTierDiscountRules(params?: {
  user_id?: number
  group_name?: string
  model?: string
}): Promise<ApiResponse<TierDiscountRule[]>> {
  const res = await api.get('/api/tier_discount/rules', { params })
  return res.data
}

export async function createTierDiscountRule(
  data: TierDiscountRuleInput
): Promise<ApiResponse<TierDiscountRule>> {
  const res = await api.post('/api/tier_discount/rules', data)
  return res.data
}

export async function updateTierDiscountRule(
  id: number,
  data: TierDiscountRuleInput
): Promise<ApiResponse<TierDiscountRule>> {
  const res = await api.put(`/api/tier_discount/rules/${id}`, data)
  return res.data
}

export async function deleteTierDiscountRule(
  id: number
): Promise<ApiResponse> {
  const res = await api.delete(`/api/tier_discount/rules/${id}`)
  return res.data
}

export async function getTierDiscountProgress(params?: {
  user_id?: number
  month?: string
}): Promise<ApiResponse<TierDiscountProgress[]>> {
  const res = await api.get('/api/tier_discount/progress', { params })
  return res.data
}

export async function getTierDiscountRebates(params?: {
  user_id?: number
  month?: string
}): Promise<ApiResponse<TierDiscountRebate[]>> {
  const res = await api.get('/api/tier_discount/rebates', { params })
  return res.data
}
