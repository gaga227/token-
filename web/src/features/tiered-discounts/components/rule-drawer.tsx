/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import type { TierDiscountRule, TierDiscountRuleInput } from '../types'

interface RuleDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  editing: TierDiscountRule | null
  onSubmit: (values: TierDiscountRuleInput) => Promise<void>
}

/** 轻量受控表单（字段少，不用 react-hook-form 全家桶；
    也因此不能使用 ui/form 的 Form* 组件——它们依赖 FormContext，会 crash） */
export function RuleDrawer({ open, onOpenChange, editing, onSubmit }: RuleDrawerProps) {
  const { t } = useTranslation()
  const [userId, setUserId] = useState('')
  const [group, setGroup] = useState('')
  const [model, setModel] = useState('')
  const [threshold, setThreshold] = useState('')
  const [discount, setDiscount] = useState('')
  const [buffer, setBuffer] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (open) {
      setError('')
      if (editing) {
        setUserId(String(editing.user_id))
        setGroup(editing.group_name)
        setModel(editing.model)
        setThreshold((editing.threshold_cents / 100).toString())
        setDiscount(String(editing.discount))
        setBuffer(String(editing.buffer_ratio || 0))
        setEnabled(editing.enabled)
      } else {
        setUserId(''); setGroup(''); setModel('')
        setThreshold(''); setDiscount(''); setBuffer('0')
        setEnabled(true)
      }
    }
  }, [open, editing])

  const submit = async () => {
    setError('')
    const uid = Number(userId)
    const th = Number(threshold)
    const dc = Number(discount)
    const bf = Number(buffer || 0)
    if (!uid || uid <= 0) return setError('用户 ID 必须为正整数')
    if (!group.trim()) return setError('分组不能为空')
    if (!model.trim()) return setError('模型名不能为空')
    if (!(th >= 0)) return setError('阈值必须 ≥ 0')
    if (!(dc > 0 && dc <= 1)) return setError('折扣必须在 (0, 1] 区间，如 0.75 表示七五折')
    if (!(bf >= 0 && bf <= 1)) return setError('滞后缓冲比例须在 [0, 1]')
    setSaving(true)
    try {
      await onSubmit({
        user_id: uid,
        group_name: group.trim(),
        model: model.trim(),
        threshold_rmb: th,
        discount: dc,
        buffer_ratio: bf,
        enabled,
      })
    } catch (e) {
      setError((e as Error).message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const fieldCls = 'space-y-2'

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className='w-[420px] sm:max-w-[420px]'>
        <SheetHeader>
          <SheetTitle>{editing ? t('编辑规则') : t('新增规则')}</SheetTitle>
          <SheetDescription>
            {t('同一用户+分组+模型可配多条阈值，构成阶梯；未达最低档按原价')}
          </SheetDescription>
        </SheetHeader>
        <div className='space-y-4 overflow-y-auto px-4'>
          <div className={fieldCls}>
            <Label htmlFor='td-user'>{t('用户 ID')}</Label>
            <Input
              id='td-user'
              placeholder='如 1'
              value={userId}
              onChange={(e) => setUserId(e.target.value.replace(/\D/g, ''))}
              disabled={!!editing}
            />
          </div>
          <div className={fieldCls}>
            <Label htmlFor='td-group'>{t('分组（渠道/线路）')}</Label>
            <Input
              id='td-group'
              placeholder='如 SeeDance2.0-kz / kimi-k3-ali'
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              disabled={!!editing}
            />
          </div>
          <div className={fieldCls}>
            <Label htmlFor='td-model'>{t('模型名（对外）')}</Label>
            <Input
              id='td-model'
              placeholder='如 doubao-seedance-2-0-260128'
              value={model}
              onChange={(e) => setModel(e.target.value)}
              disabled={!!editing}
            />
          </div>
          <div className={fieldCls}>
            <Label htmlFor='td-threshold'>{t('累计达到（元）')}</Label>
            <Input
              id='td-threshold'
              type='number'
              min={0}
              placeholder='如 1000000 表示 100 万元'
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
            />
            <p className='text-muted-foreground text-xs'>0 表示首笔即按此折扣</p>
          </div>
          <div className={fieldCls}>
            <Label htmlFor='td-discount'>{t('折扣')}</Label>
            <Input
              id='td-discount'
              type='number'
              step='0.01'
              min={0.01}
              max={1}
              placeholder='0.75 表示七五折'
              value={discount}
              onChange={(e) => setDiscount(e.target.value)}
            />
          </div>
          <div className={fieldCls}>
            <Label htmlFor='td-buffer'>{t('滞后缓冲比例')}</Label>
            <Input
              id='td-buffer'
              type='number'
              step='0.05'
              min={0}
              max={1}
              placeholder='0.1 = 超过阈值 10% 才确认升档'
              value={buffer}
              onChange={(e) => setBuffer(e.target.value)}
            />
            <p className='text-muted-foreground text-xs'>
              防止视频任务预扣/退款造成累计抖动反复触发，默认 0
            </p>
          </div>
          <div className='flex items-center justify-between rounded-lg border p-3'>
            <Label htmlFor='td-enabled'>{t('启用')}</Label>
            <Switch id='td-enabled' checked={enabled} onCheckedChange={setEnabled} />
          </div>
          {error && <p className='text-destructive text-sm'>{error}</p>}
        </div>
        <SheetFooter className='mt-6'>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={submit} disabled={saving}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
