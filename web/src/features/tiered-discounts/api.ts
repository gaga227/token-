/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { api } from '@/lib/api'

import type {
  ApiResponse,
  SimpleChannel,
  SimpleUser,
  TierRule,
  TierRuleInput,
  TierUsageMonthly,
} from './types'

export async function getTierRules(params?: {
  user_id?: number
  channel_id?: number
  model?: string
}): Promise<ApiResponse<TierRule[]>> {
  const res = await api.get('/api/tier_discount/rules', { params })
  return res.data
}

export async function createTierRule(
  data: TierRuleInput
): Promise<ApiResponse<TierRule>> {
  const res = await api.post('/api/tier_discount/rules', data)
  return res.data
}

export async function updateTierRule(
  id: number,
  data: TierRuleInput
): Promise<ApiResponse<TierRule>> {
  const res = await api.put(`/api/tier_discount/rules/${id}`, data)
  return res.data
}

export async function deleteTierRule(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/tier_discount/rules/${id}`)
  return res.data
}

/** 月度汇总（用户×渠道×模型 当月净实耗），month 形如 2026-09，缺省当月 */
export async function getTierUsage(params?: {
  user_id?: number
  channel_id?: number
  model?: string
  month?: string
}): Promise<ApiResponse<{ month: string; items: TierUsageMonthly[] }>> {
  const res = await api.get('/api/tier_discount/usage', { params })
  return res.data
}

/** 手动触发对账重算（全部规则身份或指定身份） */
export async function recalcTierUsage(params?: {
  user_id?: number
  channel_id?: number
  model?: string
  month?: string
}): Promise<ApiResponse> {
  const res = await api.post('/api/tier_discount/recalc', null, { params })
  return res.data
}

// ============================================================================
// 表单下拉数据源（复用现有用户/渠道接口，不新增后端）
// ============================================================================

const LIST_PAGE_SIZE = 200
const LIST_MAX_PAGES = 10

/** 从 {items,total} 或裸数组里取出条目 */
function pickItems<T>(payload: unknown): { items: T[]; total: number | null } {
  if (Array.isArray(payload)) {
    return { items: payload as T[], total: payload.length }
  }
  if (payload && typeof payload === 'object') {
    const obj = payload as { items?: T[]; data?: T[]; total?: number }
    const items = obj.items ?? obj.data ?? []
    return { items, total: typeof obj.total === 'number' ? obj.total : null }
  }
  return { items: [], total: 0 }
}

/** 全量用户（分页拉取，供多选下拉使用） */
export async function listSimpleUsers(): Promise<SimpleUser[]> {
  const out: SimpleUser[] = []
  for (let p = 1; p <= LIST_MAX_PAGES; p++) {
    const res = await api.get('/api/user/', {
      params: { p, page_size: LIST_PAGE_SIZE },
    })
    const { items, total } = pickItems<SimpleUser>(res.data?.data)
    out.push(
      ...items.map((u) => ({
        id: u.id,
        username: u.username,
        display_name: u.display_name,
      }))
    )
    if (items.length === 0) break
    if (total !== null && out.length >= total) break
    if (items.length < LIST_PAGE_SIZE) break
  }
  return out
}

/** 全量渠道（含 models 字段，供二级联动使用） */
export async function listSimpleChannels(): Promise<SimpleChannel[]> {
  const out: SimpleChannel[] = []
  for (let p = 1; p <= LIST_MAX_PAGES; p++) {
    const res = await api.get('/api/channel/', {
      params: { p, page_size: LIST_PAGE_SIZE },
    })
    const { items, total } = pickItems<SimpleChannel>(res.data?.data)
    out.push(
      ...items.map((c) => ({
        id: c.id,
        name: c.name,
        models: c.models ?? '',
        status: c.status,
      }))
    )
    if (items.length === 0) break
    if (total !== null && out.length >= total) break
    if (items.length < LIST_PAGE_SIZE) break
  }
  return out
}
