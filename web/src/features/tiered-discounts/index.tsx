/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Percent, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  createTierRule,
  deleteTierRule,
  getTierRules,
  getTierUsage,
  listSimpleChannels,
  listSimpleUsers,
  recalcTierUsage,
  updateTierRule,
} from './api'
import { RuleDrawer } from './components/rule-drawer'
import type { BatchSaveResult, TierRule, TierRuleInput } from './types'

const ALL = 'all'

const rmbLabel = (rmb: number) => `¥${rmb.toFixed(2).replace(/\.?0+$/, '')}`
const discountLabel = (d: number) =>
  d >= 1 ? '原价' : `${(d * 10).toFixed(2).replace(/\.?0+$/, '')} 折`

export function TieredDiscounts() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<TierRule | null>(null)
  const [filterValue, setFilterValue] = useState(ALL)

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['tier-rules'] })
    qc.invalidateQueries({ queryKey: ['tier-usage'] })
  }

  // 下拉数据源（用户多选 / 渠道单选 / 模型二级联动）
  const usersQuery = useQuery({
    queryKey: ['tier-users'],
    queryFn: listSimpleUsers,
    staleTime: 5 * 60 * 1000,
  })
  const channelsQuery = useQuery({
    queryKey: ['tier-channels'],
    queryFn: listSimpleChannels,
    staleTime: 5 * 60 * 1000,
  })
  const users = usersQuery.data || []
  const channels = channelsQuery.data || []
  const optionsLoading = usersQuery.isLoading || channelsQuery.isLoading

  const userMap = useMemo(
    () =>
      new Map(users.map((u) => [u.id, u.display_name || u.username] as const)),
    [users]
  )
  const channelMap = useMemo(
    () => new Map(channels.map((c) => [c.id, c.name] as const)),
    [channels]
  )
  const userFilterItems = useMemo(
    () => [
      { label: t('全部用户'), value: ALL },
      ...users.map((u) => ({
        label: `#${u.id} ${u.display_name || u.username}`,
        value: String(u.id),
      })),
    ],
    [users, t]
  )

  const filterUserId = filterValue === ALL ? '' : filterValue

  const rulesQuery = useQuery({
    queryKey: ['tier-rules', filterUserId],
    queryFn: () =>
      getTierRules(
        filterUserId ? { user_id: Number(filterUserId) } : undefined
      ),
  })
  const usageQuery = useQuery({
    queryKey: ['tier-usage', filterUserId],
    queryFn: () =>
      getTierUsage(
        filterUserId ? { user_id: Number(filterUserId) } : undefined
      ),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteTierRule(id),
    onSuccess: () => {
      toast.success(t('Deleted'))
      invalidate()
    },
    onError: (e: Error) => toast.error(e.message),
  })

  const recalcMutation = useMutation({
    mutationFn: () => recalcTierUsage(),
    onSuccess: () => {
      toast.success(t('对账重算完成'))
      qc.invalidateQueries({ queryKey: ['tier-usage'] })
    },
    onError: (e: Error) => toast.error(e.message),
  })

  /** 批量保存：新增时按「用户 × 模型」展开，已存在的组合自动跳过 */
  const handleSubmit = async (
    list: TierRuleInput[]
  ): Promise<BatchSaveResult> => {
    if (editing) {
      await updateTierRule(editing.id, list[0])
      invalidate()
      return { created: 1, skipped: 0, failed: [] }
    }
    // 唯一键是「用户+渠道+模型+阈值」，同一身份可配多条不同阈值构成阶梯，
    // 所以去重必须带上阈值（分）。用全量规则判断，避免受当前筛选影响。
    const keyOf = (
      userId: number,
      channelId: number,
      model: string,
      thresholdRmb: number
    ) => `${userId}|${channelId}|${model}|${Math.round(thresholdRmb * 100)}`
    let existing = new Set<string>()
    try {
      const all = await getTierRules()
      existing = new Set(
        (all.data || []).map((r) =>
          keyOf(r.user_id, r.channel_id, r.model, r.threshold_rmb)
        )
      )
    } catch {
      // 拉取失败不阻塞创建，退化为不去重（后端唯一键会兜住）
    }
    let created = 0
    let skipped = 0
    const failed: string[] = []
    for (const payload of list) {
      const key = keyOf(
        payload.user_id,
        payload.channel_id,
        payload.model,
        payload.threshold_rmb
      )
      if (existing.has(key)) {
        skipped++
        continue
      }
      try {
        await createTierRule(payload)
        existing.add(key)
        created++
      } catch (e) {
        failed.push(`#${payload.user_id} / ${payload.model}: ${(e as Error).message}`)
      }
    }
    if (created) invalidate()
    return { created, skipped, failed }
  }

  const rules = rulesQuery.data?.data || []
  const usage = usageQuery.data?.data?.items || []
  const usageMonth = usageQuery.data?.data?.month || ''

  const identityLabel = (userId: number, channelId: number, model: string) => (
    <>
      {t('用户')} #{userId}
      {userMap.get(userId) ? (
        <span className='text-muted-foreground'> {userMap.get(userId)}</span>
      ) : null}
      {' · '}
      {t('渠道')} #{channelId}
      {channelMap.get(channelId) ? (
        <span className='text-muted-foreground'> {channelMap.get(channelId)}</span>
      ) : null}
      {' · '}
      {model}
    </>
  )

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Tiered Discounts')}
          <span className='text-muted-foreground ml-2 text-sm font-normal'>
            {t(
              '按「用户 × 渠道 × 模型」配置阶梯折扣：扣费时按当月累计实耗取档打折，未配置的身份按原价'
            )}
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Select
            items={userFilterItems}
            value={filterValue}
            onValueChange={(v) => setFilterValue(v ?? ALL)}
          >
            <SelectTrigger className='w-60'>
              <SelectValue placeholder={t('全部用户')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {userFilterItems.map((u) => (
                  <SelectItem key={u.value} value={u.value}>
                    {u.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Button
            onClick={() => {
              setEditing(null)
              setDrawerOpen(true)
            }}
          >
            <Percent className='mr-2 h-4 w-4' />
            {t('新增档位')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Tabs defaultValue='rules'>
            <TabsList>
              <TabsTrigger value='rules'>
                {t('档位配置')} ({rules.length})
              </TabsTrigger>
              <TabsTrigger value='usage'>
                {t('当月汇总')} ({usage.length})
              </TabsTrigger>
            </TabsList>

            {/* 档位配置 */}
            <TabsContent value='rules' className='mt-4 space-y-3'>
              {rules.length === 0 && (
                <Card>
                  <CardContent className='text-muted-foreground py-10 text-center'>
                    {t(
                      '暂无档位，点击「新增档位」创建第一套阶梯；未配置的用户/渠道/模型一律原价'
                    )}
                  </CardContent>
                </Card>
              )}
              {rules.map((r) => (
                <Card key={r.id}>
                  <CardContent className='flex flex-wrap items-center gap-3 py-4'>
                    <Badge variant={r.enabled ? 'default' : 'secondary'}>
                      {r.enabled ? t('启用') : t('停用')}
                    </Badge>
                    <div className='min-w-0 flex-1'>
                      <div className='truncate font-medium'>
                        {identityLabel(r.user_id, r.channel_id, r.model)}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        累计 ≥ {rmbLabel(r.threshold_rmb)} →{' '}
                        {discountLabel(r.discount)}
                        {r.remark && ` · ${r.remark}`}
                      </div>
                    </div>
                    <div className='flex gap-2'>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          setEditing(r)
                          setDrawerOpen(true)
                        }}
                      >
                        {t('Edit')}
                      </Button>
                      <Button
                        variant='destructive'
                        size='sm'
                        onClick={() => {
                          if (confirm(t('确认删除该档位？已享折扣不收回')))
                            deleteMutation.mutate(r.id)
                        }}
                      >
                        {t('Delete')}
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </TabsContent>

            {/* 当月汇总 */}
            <TabsContent value='usage' className='mt-4 space-y-2'>
              <div className='flex items-center justify-between'>
                <span className='text-muted-foreground text-sm'>
                  {usageMonth
                    ? t('按「消费 − 退款」净额统计（跨月自动归档）')
                    : ''}
                </span>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={recalcMutation.isPending}
                  onClick={() => recalcMutation.mutate()}
                >
                  <RefreshCw className='mr-2 h-4 w-4' />
                  {t('对账重算')}
                </Button>
              </div>
              {usage.length === 0 && (
                <Card>
                  <CardContent className='text-muted-foreground py-10 text-center'>
                    {t('本月暂无消费')}
                  </CardContent>
                </Card>
              )}
              {usage.map((u) => (
                <Card key={u.id}>
                  <CardContent className='flex flex-wrap items-center gap-3 py-3 text-sm'>
                    <span className='font-medium'>
                      {identityLabel(u.user_id, u.channel_id, u.model)}
                    </span>
                    <span className='text-muted-foreground'>{u.month}</span>
                    <span className='ml-auto'>
                      净实耗 <b>{rmbLabel(u.consumed_rmb)}</b>
                    </span>
                  </CardContent>
                </Card>
              ))}
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      {/* ⚠️ Dialogs 必须放在 SectionPageLayout 外：
        该布局只渲染四个具名插槽的子节点，其余直接子节点会被丢弃 */}
      <RuleDrawer
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        editing={editing}
        users={users}
        channels={channels}
        optionsLoading={optionsLoading}
        onSubmit={handleSubmit}
      />
    </>
  )
}
