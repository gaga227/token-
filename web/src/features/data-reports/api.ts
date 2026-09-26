/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { api } from '@/lib/api'
import dayjs from '@/lib/dayjs'

import {
  listSimpleChannels,
  listSimpleUsers,
} from '@/features/tiered-discounts/api'
import type { SimpleChannel, SimpleUser } from '@/features/tiered-discounts/types'

export { listSimpleChannels, listSimpleUsers }
export type { SimpleChannel, SimpleUser }

import type {
  ReportDetailsData,
  ReportSummaryData,
  SummaryDim,
} from './types'

type ApiResp<T> = { success: boolean; message: string; data: T }

/** 报表筛选（多维多选 + 北京时间日期范围；日期允许 Date 或 YYYY-MM-DD） */
export interface ReportFilterParams {
  userIds: string[]
  channelIds: string[]
  models: string[]
  startDate?: Date | string
  endDate?: Date | string
  includeErrors: boolean
  group: string
  tokenName: string
}

export const defaultReportFilters = (): ReportFilterParams => ({
  userIds: [],
  channelIds: [],
  models: [],
  includeErrors: false,
  group: '',
  tokenName: '',
})

const dateStr = (v: Date | string | undefined) =>
  v ? dayjs(v).format('YYYY-MM-DD') : undefined

/** 筛选条件 → 请求参数（供请求与 queryKey 共用） */
export function filterToQuery(f: ReportFilterParams): Record<string, string> {
  const q: Record<string, string> = {}
  if (f.userIds.length) q.user_ids = f.userIds.join(',')
  if (f.channelIds.length) q.channel_ids = f.channelIds.join(',')
  if (f.models.length) q.models = f.models.join(',')
  const sd = dateStr(f.startDate)
  const ed = dateStr(f.endDate)
  if (sd) q.start_date = sd
  if (ed) q.end_date = ed
  // 净额口径默认 消耗+退还；勾选后带出失败请求
  q.types = f.includeErrors ? '2,5,6' : '2,6'
  if (f.group) q.group = f.group
  if (f.tokenName) q.token_name = f.tokenName
  return q
}

/** 报表1：使用明细（分页 + 合计行） */
export async function getReportDetails(
  f: ReportFilterParams,
  page: number,
  pageSize: number
): Promise<ApiResp<ReportDetailsData>> {
  const res = await api.get('/api/report/details', {
    params: { ...filterToQuery(f), p: page, page_size: pageSize },
  })
  return res.data
}

/** 报表2：使用汇总（维度可切换） */
export async function getReportSummary(
  f: ReportFilterParams,
  dim: SummaryDim
): Promise<ApiResp<ReportSummaryData>> {
  const res = await api.get('/api/report/summary', {
    params: { ...filterToQuery(f), dim },
  })
  return res.data
}

/** CSV 导出走异步任务（后端限速后台生成，24h 内取件） */
export interface ReportExportTask {
  id: string
  type: 'details' | 'summary'
  status: 'pending' | 'running' | 'done' | 'failed'
  rows: number
  error?: string
  filename?: string
}

/** 提交导出任务，返回任务 id */
export async function submitReportExport(
  kind: 'details' | 'summary',
  f: ReportFilterParams,
  dim?: SummaryDim
): Promise<string> {
  const qs = new URLSearchParams(filterToQuery(f))
  qs.set('type', kind)
  if (kind === 'summary' && dim) qs.set('dim', dim)
  const res = await api.post(`/api/report/export?${qs.toString()}`)
  if (!res.data.success) throw new Error(res.data.message || '提交失败')
  return res.data.data.id as string
}

/** 查询任务进度 */
export async function getReportExportStatus(
  id: string
): Promise<ReportExportTask> {
  const res = await api.get('/api/report/export/status', { params: { id } })
  if (!res.data.success) throw new Error(res.data.message || '查询失败')
  return res.data.data as ReportExportTask
}

/** 下载已完成任务的 CSV */
export async function downloadReportExport(id: string): Promise<void> {
  const res = await api.get('/api/report/export/download', {
    params: { id },
    responseType: 'blob',
  })
  const blob = new Blob([res.data as BlobPart])
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  // 从 Content-Disposition 取文件名（gin 输出 filename="xxx.csv" 带引号，需剥掉）
  const cd = (res.headers['content-disposition'] as string | undefined) || ''
  const m = cd.match(/filename="?([^";]+)"?/)
  a.download = m ? m[1] : `report_${id}.csv`
  a.href = url
  a.click()
  URL.revokeObjectURL(url)
}
