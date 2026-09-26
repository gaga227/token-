/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { useQuery } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import dayjs from '@/lib/dayjs'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  defaultReportFilters,
  downloadReportExport,
  getReportDetails,
  getReportExportStatus,
  getReportSummary,
  listSimpleChannels,
  listSimpleUsers,
  submitReportExport,
  type ReportFilterParams,
} from './api'
import {
  ReportFilterBar,
  filtersToApiQuery,
} from './components/report-filter-bar'
import type { ReportDetailRow, ReportSummaryRow, SummaryDim } from './types'

const PAGE_SIZE = 50

/** quota → 元，保留最多 4 位小数并去尾零 */
function money(quota: number, rate: number): string {
  const s = (quota * rate).toFixed(4)
  return `¥${s.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '')}`
}

function typeBadge(type: number, t: (k: string) => string) {
  if (type === 6)
    return (
      <Badge variant='outline' className='text-amber-600'>
        {t('退还')}
      </Badge>
    )
  if (type === 5)
    return (
      <Badge variant='destructive'>{t('失败')}</Badge>
    )
  return <Badge variant='secondary'>{t('消耗')}</Badge>
}

export function DataReports() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<'details' | 'summary'>('details')
  const [filters, setFilters] = useState<ReportFilterParams>(() => ({
    ...defaultReportFilters(),
    startDate: dayjs().startOf('month').toDate(),
    endDate: dayjs().startOf('day').toDate(),
  }))
  const [page, setPage] = useState(1)
  const [dim, setDim] = useState<SummaryDim>('user_channel_model')

  // 下拉数据源（用户/渠道全量；模型随渠道联动）
  const usersQuery = useQuery({
    queryKey: ['report-users'],
    queryFn: listSimpleUsers,
    staleTime: 5 * 60 * 1000,
  })
  const channelsQuery = useQuery({
    queryKey: ['report-channels'],
    queryFn: listSimpleChannels,
    staleTime: 5 * 60 * 1000,
  })
  const users = usersQuery.data || []
  const channels = channelsQuery.data || []

  const userOptions = useMemo(
    () =>
      users.map((u) => ({
        value: String(u.id),
        label: `#${u.id} ${u.display_name || u.username}`,
      })),
    [users]
  )
  const channelOptions = useMemo(
    () =>
      channels.map((c) => ({ value: String(c.id), label: `#${c.id} ${c.name}` })),
    [channels]
  )
  // 模型候选：选了渠道 → 这几个渠道的模型并集；否则全部渠道模型并集
  const modelOptions = useMemo(() => {
    const picked = filters.channelIds.map(Number)
    const src =
      picked.length > 0
        ? channels.filter((c) => picked.includes(c.id))
        : channels
    const set = new Set<string>()
    for (const c of src) {
      for (const m of (c.models || '').split(',')) {
        const v = m.trim()
        if (v) set.add(v)
      }
    }
    return Array.from(set)
      .sort()
      .map((m) => ({ value: m, label: m }))
  }, [channels, filters.channelIds])

  const queryKeyBase = useMemo(
    () => JSON.stringify(filtersToApiQuery(filters)),
    [filters]
  )
  useEffect(() => {
    setPage(1)
  }, [queryKeyBase])

  const detailsQuery = useQuery({
    queryKey: ['report-details', queryKeyBase, page],
    queryFn: () => getReportDetails(filters, page, PAGE_SIZE),
    enabled: tab === 'details',
    placeholderData: (prev) => prev,
  })
  const summaryQuery = useQuery({
    queryKey: ['report-summary', queryKeyBase, dim],
    queryFn: () => getReportSummary(filters, dim),
    enabled: tab === 'summary',
    placeholderData: (prev) => prev,
  })

  const details = detailsQuery.data?.data
  const rows = details?.items || []
  const totals = details?.totals
  const rate = details?.rmb_rate || 0
  const total = details?.total || 0
  const maxPage = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const summary = summaryQuery.data?.data
  const summaryRows = summary?.items || []
  const summaryRate = summary?.rmb_rate || 0

  // 异步导出：提交任务 → 轮询进度 → 完成自动下载
  const [exportState, setExportState] = useState<{
    status: 'submitting' | 'pending' | 'running' | 'done'
    rows: number
  } | null>(null)
  const exportActiveRef = useRef(false)

  const pollExport = async (id: string) => {
    while (exportActiveRef.current) {
      await new Promise((r) => setTimeout(r, 1500))
      if (!exportActiveRef.current) return
      try {
        const task = await getReportExportStatus(id)
        if (task.status === 'done') {
          setExportState({ status: 'done', rows: task.rows })
          exportActiveRef.current = false
          await downloadReportExport(id)
          toast.success(t('导出完成，已开始下载'))
          setTimeout(() => setExportState(null), 3000)
          return
        }
        if (task.status === 'failed') {
          exportActiveRef.current = false
          setExportState(null)
          toast.error(t('导出失败') + ': ' + (task.error || '未知错误'))
          return
        }
        setExportState({ status: task.status, rows: task.rows })
      } catch (e) {
        exportActiveRef.current = false
        setExportState(null)
        toast.error(t('导出失败') + ': ' + (e as Error).message)
        return
      }
    }
  }

  const handleExport = async () => {
    if (exportActiveRef.current) return
    exportActiveRef.current = true
    setExportState({ status: 'submitting', rows: 0 })
    try {
      const id =
        tab === 'details'
          ? await submitReportExport('details', filters)
          : await submitReportExport('summary', filters, dim)
      await pollExport(id)
    } catch (e) {
      exportActiveRef.current = false
      setExportState(null)
      toast.error(t('提交导出失败') + ': ' + (e as Error).message)
    }
  }

  // 页面卸载时停止轮询（任务仍在后台继续，不影响结果）
  useEffect(
    () => () => {
      exportActiveRef.current = false
    },
    []
  )

  const exportLabel = exportState
    ? exportState.status === 'running'
      ? t('已导出 {{rows}} 行…', { rows: exportState.rows })
      : t('处理中…')
    : undefined

  // 汇总行下钻：带入该行身份到明细
  const drillDown = (r: ReportSummaryRow) => {
    setFilters((f) => ({
      ...f,
      userIds: [String(r.user_id)],
      channelIds: r.channel_id ? [String(r.channel_id)] : [],
      models: r.model ? [r.model] : [],
    }))
    setTab('details')
  }

  const userName = (id: number, fallback: string) => {
    const u = users.find((x) => x.id === id)
    return u ? `#${id} ${u.display_name || u.username}` : `#${id} ${fallback}`
  }
  const channelLabel = (id: number, fallback: string) => {
    if (!id) return '—'
    const c = channels.find((x) => x.id === id)
    return `#${id} ${c?.name || fallback || ''}`
  }

  const dimLabel: Record<SummaryDim, string> = {
    user: t('按用户'),
    user_channel: t('按用户+渠道'),
    user_channel_model: t('按用户+渠道+模型'),
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Data Reports')}
          <span className='text-muted-foreground ml-2 text-sm font-normal'>
            {t(
              '运营对账：明细全量展示（退还记负、失败标注），汇总按净额口径冲减，与用户余额变动可对上'
            )}
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='space-y-4'>
            <Card>
              <CardContent className='py-4'>
                <ReportFilterBar
                  filters={filters}
                  onChange={setFilters}
                  userOptions={userOptions}
                  channelOptions={channelOptions}
                  modelOptions={modelOptions}
                  loading={usersQuery.isLoading || channelsQuery.isLoading}
                  onExport={handleExport}
                  exporting={!!exportState}
                  exportLabel={exportLabel}
                  showIncludeErrors={tab === 'details'}
                />
              </CardContent>
            </Card>

            <Tabs value={tab} onValueChange={(v) => setTab(v as 'details' | 'summary')}>
              <TabsList>
                <TabsTrigger value='details'>{t('使用明细')}</TabsTrigger>
                <TabsTrigger value='summary'>{t('使用汇总')}</TabsTrigger>
              </TabsList>

              {/* 报表1：使用明细 */}
              <TabsContent value='details' className='mt-4 space-y-3'>
                <Card>
                  <CardContent className='p-0'>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t('时间')}</TableHead>
                          <TableHead>{t('类型')}</TableHead>
                          <TableHead>{t('用户')}</TableHead>
                          <TableHead>{t('渠道')}</TableHead>
                          <TableHead>{t('模型')}</TableHead>
                          <TableHead>{t('令牌')}</TableHead>
                          <TableHead className='text-right'>
                            {t('输入 tokens')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('输出 tokens')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('原价')}
                          </TableHead>
                          <TableHead title={t('当时实际生效的阶梯折扣')}>
                            {t('折扣')}
                          </TableHead>
                          <TableHead
                            className='text-right'
                            title={t('当时实际扣费金额')}
                          >
                            {t('实付')}
                          </TableHead>
                          <TableHead title={t('满足阶梯档位时显示配置的阶梯折扣')}>
                            {t('阶梯折扣')}
                          </TableHead>
                          <TableHead
                            className='text-right'
                            title={t('按档位折扣计算出的实扣金额')}
                          >
                            {t('阶梯实付')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('耗时')}
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {detailsQuery.isLoading && (
                          <TableRow>
                            <TableCell colSpan={14} className='py-10 text-center'>
                              <Loader2 className='mx-auto h-5 w-5 animate-spin' />
                            </TableCell>
                          </TableRow>
                        )}
                        {!detailsQuery.isLoading && rows.length === 0 && (
                          <TableRow>
                            <TableCell
                              colSpan={14}
                              className='text-muted-foreground py-10 text-center'
                            >
                              {t('当前筛选条件下没有记录')}
                            </TableCell>
                          </TableRow>
                        )}
                        {rows.map((r: ReportDetailRow) => (
                          <TableRow key={`${r.id}-${r.created_at}`}>
                            <TableCell className='whitespace-nowrap'>
                              {dayjs(r.created_at * 1000).format(
                                'MM-DD HH:mm:ss'
                              )}
                            </TableCell>
                            <TableCell>{typeBadge(r.type, t)}</TableCell>
                            <TableCell>
                              {userName(r.user_id, r.username)}
                            </TableCell>
                            <TableCell>
                              {channelLabel(r.channel, r.channel_name)}
                            </TableCell>
                            <TableCell className='max-w-48 truncate'>
                              {r.model_name || '—'}
                            </TableCell>
                            <TableCell className='max-w-28 truncate'>
                              {r.token_name || '—'}
                            </TableCell>
                            <TableCell className='text-right'>
                              {r.prompt_tokens || '—'}
                            </TableCell>
                            <TableCell className='text-right'>
                              {r.completion_tokens || '—'}
                            </TableCell>
                            <TableCell className='text-right'>
                              {rate ? money(r.origin_quota, rate) : '—'}
                            </TableCell>
                            {/* 折扣：当时实际生效的阶梯折扣 */}
                            <TableCell>
                              {r.discount > 0 ? (
                                <Badge
                                  variant='outline'
                                  className='border-teal-300 bg-teal-50 text-teal-700'
                                  title={`折扣率 ${(r.discount * 100).toFixed(0)}%`}
                                >
                                  {(r.discount * 10).toFixed(1).replace(/\.0$/, '')} 折
                                </Badge>
                              ) : (
                                <span className='text-muted-foreground'>—</span>
                              )}
                            </TableCell>
                            {/* 实付：当时实际扣费金额 */}
                            <TableCell
                              className={`text-right font-medium ${
                                r.paid_quota < 0
                                  ? 'text-amber-600'
                                  : r.discount > 0
                                    ? 'text-teal-700'
                                    : ''
                              }`}
                            >
                              {rate ? money(r.paid_quota, rate) : '—'}
                            </TableCell>
                            {/* 阶梯折扣：满足档位时显示配置的阶梯折扣 */}
                            <TableCell>
                              {r.discount > 0 ? (
                                <Badge
                                  className='bg-teal-600 text-white'
                                  title={`档位折扣率 ${(r.discount * 100).toFixed(0)}%`}
                                >
                                  {(r.discount * 10).toFixed(1).replace(/\.0$/, '')} 折
                                </Badge>
                              ) : (
                                <span className='text-muted-foreground'>—</span>
                              )}
                            </TableCell>
                            {/* 阶梯实付：按档位折扣计算出的实扣 */}
                            <TableCell
                              className={`text-right ${
                                r.discount > 0 && r.origin_quota < 0
                                  ? 'text-amber-600'
                                  : ''
                              }`}
                            >
                              {rate && r.discount > 0
                                ? money(Math.round(r.origin_quota * r.discount), rate)
                                : '—'}
                            </TableCell>
                            <TableCell className='text-right'>
                              {r.use_time ? `${r.use_time}s` : '—'}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                      {totals && rows.length > 0 && (
                        <TableFooter>
                          <TableRow>
                            <TableCell colSpan={6}>
                              {t('合计')}：{totals.requests} {t('笔消耗')} /{' '}
                              {totals.refunds} {t('笔退还')}
                              {totals.discount_quota !== 0 && (
                                <span className='text-muted-foreground'>
                                  {' '}
                                  · {t('阶梯优惠')}{' '}
                                  {rate
                                    ? money(totals.discount_quota, rate)
                                    : '—'}
                                </span>
                              )}
                            </TableCell>
                            <TableCell className='text-right'>
                              {totals.prompt_tokens}
                            </TableCell>
                            <TableCell className='text-right'>
                              {totals.completion_tokens}
                            </TableCell>
                            <TableCell className='text-right'>
                              {rate ? money(totals.origin_quota, rate) : '—'}
                            </TableCell>
                            <TableCell />
                            <TableCell
                              className={`text-right font-semibold ${
                                totals.paid_quota < 0 ? 'text-amber-600' : ''
                              }`}
                            >
                              {rate ? money(totals.paid_quota, rate) : '—'}
                            </TableCell>
                            <TableCell />
                            <TableCell className='text-right'>
                              {rate ? money(totals.tier_paid_quota, rate) : '—'}
                            </TableCell>
                            <TableCell />
                          </TableRow>
                        </TableFooter>
                      )}
                    </Table>
                  </CardContent>
                </Card>
                <div className='flex items-center justify-end gap-2'>
                  <span className='text-muted-foreground text-sm'>
                    {t('共')} {total} {t('条')} · {t('第')} {page}/{maxPage}{' '}
                    {t('页')}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page <= 1 || detailsQuery.isFetching}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    {t('上一页')}
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page >= maxPage || detailsQuery.isFetching}
                    onClick={() => setPage((p) => Math.min(maxPage, p + 1))}
                  >
                    {t('下一页')}
                  </Button>
                </div>
              </TabsContent>

              {/* 报表2：使用汇总 */}
              <TabsContent value='summary' className='mt-4 space-y-3'>
                <div className='flex items-center gap-2'>
                  <span className='text-muted-foreground text-sm'>
                    {t('汇总维度')}
                  </span>
                  <Select
                    value={dim}
                    onValueChange={(v) => setDim(v as SummaryDim)}
                  >
                    <SelectTrigger className='w-56'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectItem value='user'>{dimLabel.user}</SelectItem>
                        <SelectItem value='user_channel'>
                          {dimLabel.user_channel}
                        </SelectItem>
                        <SelectItem value='user_channel_model'>
                          {dimLabel.user_channel_model}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <span className='text-muted-foreground text-xs'>
                    {t('点击任意行可下钻到对应明细')}
                  </span>
                </div>
                <Card>
                  <CardContent className='p-0'>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t('用户')}</TableHead>
                          {dim !== 'user' && <TableHead>{t('渠道')}</TableHead>}
                          {dim === 'user_channel_model' && (
                            <TableHead>{t('模型')}</TableHead>
                          )}
                          <TableHead className='text-right'>
                            {t('请求数')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('退还笔数')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('输入 tokens')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('输出 tokens')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('原价')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('阶梯优惠')}
                          </TableHead>
                          <TableHead className='text-right'>
                            {t('阶梯实付')}
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {summaryQuery.isLoading && (
                          <TableRow>
                            <TableCell colSpan={10} className='py-10 text-center'>
                              <Loader2 className='mx-auto h-5 w-5 animate-spin' />
                            </TableCell>
                          </TableRow>
                        )}
                        {!summaryQuery.isLoading &&
                          summaryRows.length === 0 && (
                            <TableRow>
                              <TableCell
                                colSpan={10}
                                className='text-muted-foreground py-10 text-center'
                              >
                                {t('当前筛选条件下没有记录')}
                              </TableCell>
                            </TableRow>
                          )}
                        {summaryRows.map(
                          (r: ReportSummaryRow, idx: number) => (
                            <TableRow
                              key={`${r.user_id}-${r.channel_id}-${r.model}-${idx}`}
                              className='cursor-pointer'
                              onClick={() => drillDown(r)}
                            >
                              <TableCell>
                                {userName(r.user_id, r.username)}
                              </TableCell>
                              {dim !== 'user' && (
                                <TableCell>
                                  {channelLabel(r.channel_id, r.channel_name)}
                                </TableCell>
                              )}
                              {dim === 'user_channel_model' && (
                                <TableCell className='max-w-48 truncate'>
                                  {r.model || '—'}
                                </TableCell>
                              )}
                              <TableCell className='text-right'>
                                {r.requests}
                              </TableCell>
                              <TableCell className='text-right'>
                                {r.refunds || '—'}
                              </TableCell>
                              <TableCell className='text-right'>
                                {r.prompt_tokens}
                              </TableCell>
                              <TableCell className='text-right'>
                                {r.completion_tokens}
                              </TableCell>
                              <TableCell className='text-right'>
                                {summaryRate
                                  ? money(r.origin_quota, summaryRate)
                                  : '—'}
                              </TableCell>
                              <TableCell className='text-right'>
                                {summaryRate && r.discount_quota !== 0
                                  ? money(r.discount_quota, summaryRate)
                                  : '—'}
                              </TableCell>
                              <TableCell className='text-right font-medium'>
                                {summaryRate
                                  ? money(r.paid_quota, summaryRate)
                                  : '—'}
                              </TableCell>
                            </TableRow>
                          )
                        )}
                      </TableBody>
                    </Table>
                  </CardContent>
                </Card>
              </TabsContent>
            </Tabs>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </>
  )
}
