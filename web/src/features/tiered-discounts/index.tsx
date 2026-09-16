/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Percent } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  createTierDiscountRule,
  deleteTierDiscountRule,
  getTierDiscountProgress,
  getTierDiscountRebates,
  getTierDiscountRules,
  updateTierDiscountRule,
} from './api'
import { RuleDrawer } from './components/rule-drawer'
import type { TierDiscountRule } from './types'

const centsToYuan = (cents: number) => (cents / 100).toFixed(2)
const discountLabel = (d: number) =>
  d >= 1 ? '原价' : `${(d * 10).toFixed(2).replace(/\.?0+$/, '')} 折`

export function TieredDiscounts() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<TierDiscountRule | null>(null)
  const [filterUserId, setFilterUserId] = useState('')

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['tier-discount-rules'] })
    qc.invalidateQueries({ queryKey: ['tier-discount-progress'] })
  }

  const rulesQuery = useQuery({
    queryKey: ['tier-discount-rules', filterUserId],
    queryFn: () =>
      getTierDiscountRules(
        filterUserId ? { user_id: Number(filterUserId) } : undefined
      ),
  })
  const progressQuery = useQuery({
    queryKey: ['tier-discount-progress', filterUserId],
    queryFn: () =>
      getTierDiscountProgress(
        filterUserId ? { user_id: Number(filterUserId) } : undefined
      ),
  })
  const rebatesQuery = useQuery({
    queryKey: ['tier-discount-rebates', filterUserId],
    queryFn: () =>
      getTierDiscountRebates(
        filterUserId ? { user_id: Number(filterUserId) } : undefined
      ),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteTierDiscountRule(id),
    onSuccess: () => {
      toast.success(t('Deleted'))
      invalidate()
    },
    onError: (e: Error) => toast.error(e.message),
  })

  const rules = rulesQuery.data?.data || []
  const progress = progressQuery.data?.data || []
  const rebates = rebatesQuery.data?.data || []

  return (
    <>
      <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        {t('Tiered Discounts')}
        <span className='text-muted-foreground ml-2 text-sm font-normal'>
          {t(
            '按「用户 × 分组 × 模型」配置阶梯折扣：当月累计消费跨档后自动切换折扣并返还差额'
          )}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Input
          placeholder='用户 ID 过滤'
          className='w-32'
          value={filterUserId}
          onChange={(e) =>
            setFilterUserId(e.target.value.replace(/\D/g, ''))
          }
        />
        <Button
          onClick={() => {
            setEditing(null)
            setDrawerOpen(true)
          }}
        >
          <Percent className='mr-2 h-4 w-4' />
          {t('新增规则')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <Tabs defaultValue='rules'>
          <TabsList>
            <TabsTrigger value='rules'>
              {t('规则')} ({rules.length})
            </TabsTrigger>
            <TabsTrigger value='progress'>
              {t('当月进度')} ({progress.length})
            </TabsTrigger>
            <TabsTrigger value='rebates'>
              {t('返还记录')} ({rebates.length})
            </TabsTrigger>
          </TabsList>

          {/* 规则 */}
          <TabsContent value='rules' className='mt-4 space-y-3'>
            {rules.length === 0 && (
              <Card>
                <CardContent className='py-10 text-center text-muted-foreground'>
                  {t('暂无规则，点击「新增规则」创建第一套阶梯')}
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
                      {t('用户')} #{r.user_id} · {r.group_name} · {r.model}
                    </div>
                    <div className='text-muted-foreground text-xs'>
                      累计 ≥ ¥{centsToYuan(r.threshold_cents)} →{' '}
                      {discountLabel(r.discount)}
                      {r.buffer_ratio > 0 &&
                        ` · 滞后缓冲 ${(r.buffer_ratio * 100).toFixed(0)}%`}
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
                        if (confirm(t('确认删除该规则？')))
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

          {/* 当月进度 */}
          <TabsContent value='progress' className='mt-4 space-y-2'>
            {progress.length === 0 && (
              <Card>
                <CardContent className='py-10 text-center text-muted-foreground'>
                  {t('本月暂无消费进度')}
                </CardContent>
              </Card>
            )}
            {progress.map((p) => (
              <Card key={p.id}>
                <CardContent className='flex flex-wrap items-center gap-3 py-3 text-sm'>
                  <span className='font-medium'>
                    #{p.user_id} · {p.group_name} · {p.model}
                  </span>
                  <span className='text-muted-foreground'>{p.month}</span>
                  <span className='ml-auto'>
                    累计{' '}
                    <b>¥{centsToYuan(p.total_cents)}</b>
                    <span className='text-muted-foreground'>
                      （现金 ¥{centsToYuan(p.cash_cents)}）
                    </span>
                  </span>
                  <Badge variant={p.current_discount < 1 ? 'default' : 'outline'}>
                    {discountLabel(p.current_discount)}
                  </Badge>
                </CardContent>
              </Card>
            ))}
          </TabsContent>

          {/* 返还记录 */}
          <TabsContent value='rebates' className='mt-4 space-y-2'>
            {rebates.length === 0 && (
              <Card>
                <CardContent className='py-10 text-center text-muted-foreground'>
                  {t('暂无返还记录')}
                </CardContent>
              </Card>
            )}
            {rebates.map((b) => (
              <Card key={b.id}>
                <CardContent className='flex flex-wrap items-center gap-3 py-3 text-sm'>
                  <span className='font-medium'>
                    #{b.user_id} · {b.group_name} · {b.model}
                  </span>
                  <span className='text-muted-foreground'>
                    {b.month} · {discountLabel(b.old_discount)} →{' '}
                    {discountLabel(b.new_discount)}
                  </span>
                  <span className='ml-auto font-semibold text-emerald-600'>
                    +¥{centsToYuan(b.rebate_cents)}
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
        onSubmit={async (values) => {
          if (editing) {
            await updateTierDiscountRule(editing.id, values)
          } else {
            await createTierDiscountRule(values)
          }
          invalidate()
          setDrawerOpen(false)
          toast.success(t('Saved'))
        }}
      />
    </>
  )
}
