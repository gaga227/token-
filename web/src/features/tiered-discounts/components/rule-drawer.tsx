/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import type {
  BatchSaveResult,
  SimpleChannel,
  SimpleUser,
  TierRule,
  TierRuleInput,
} from '../types'

interface RuleDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  editing: TierRule | null
  users: SimpleUser[]
  channels: SimpleChannel[]
  optionsLoading: boolean
  onSubmit: (values: TierRuleInput[]) => Promise<BatchSaveResult>
}

const SPLIT_REGEX = /[,，\n]/

/** 轻量受控表单（字段少，不用 react-hook-form 全家桶；
    也因此不能使用 ui/form 的 Form* 组件——它们依赖 FormContext，会 crash） */
export function RuleDrawer({
  open,
  onOpenChange,
  editing,
  users,
  channels,
  optionsLoading,
  onSubmit,
}: RuleDrawerProps) {
  const { t } = useTranslation()
  const [userIds, setUserIds] = useState<string[]>([])
  const [channelId, setChannelId] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [threshold, setThreshold] = useState('')
  const [discount, setDiscount] = useState('')
  const [remark, setRemark] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const isEditing = !!editing

  useEffect(() => {
    if (!open) return
    setError('')
    if (editing) {
      setUserIds([String(editing.user_id)])
      setChannelId(String(editing.channel_id))
      setModels([editing.model])
      setThreshold(
        editing.threshold_rmb != null
          ? String(Number(editing.threshold_rmb.toFixed(2)))
          : '0'
      )
      setDiscount(String(editing.discount))
      setRemark(editing.remark || '')
      setEnabled(editing.enabled)
    } else {
      setUserIds([])
      setChannelId('')
      setModels([])
      setThreshold('')
      setDiscount('')
      setRemark('')
      setEnabled(true)
    }
  }, [open, editing])

  const userOptions = useMemo(
    () =>
      users.map((u) => ({
        value: String(u.id),
        label: `#${u.id} ${u.display_name || u.username}`,
      })),
    [users]
  )

  const channelOptions = useMemo(
    () => channels.map((c) => ({ value: String(c.id), label: `#${c.id} ${c.name}` })),
    [channels]
  )

  // 二级联动：模型选项来自所选渠道的 models 字段（逗号分隔）
  const modelOptions = useMemo(() => {
    const channel = channels.find((c) => String(c.id) === channelId)
    if (!channel) return []
    const set = new Set<string>()
    for (const raw of (channel.models || '').split(SPLIT_REGEX)) {
      const name = raw.trim()
      if (name) set.add(name)
    }
    return Array.from(set)
      .sort()
      .map((name) => ({ value: name, label: name }))
  }, [channels, channelId])

  const selectedChannel = channels.find((c) => String(c.id) === channelId)

  const applyChannel = (value: string | null) => {
    const next = value ?? ''
    if (next !== channelId) {
      // 渠道变了，原模型选项可能不在新渠道下，清空避免配出无效组合
      setModels([])
    }
    setChannelId(next)
  }

  const submit = async () => {
    setError('')
    const uidList = userIds.map(Number).filter((n) => n > 0)
    const ch = Number(channelId)
    const modelList = models.map((m) => m.trim()).filter(Boolean)
    if (uidList.length === 0) return setError(t('请选择用户'))
    if (!ch || ch <= 0)
      return setError(
        t('请选择渠道（渠道删除重建后 ID 会变，需重新配置规则）')
      )
    if (modelList.length === 0) return setError(t('请选择模型'))
    const th = Number(threshold)
    const dc = Number(discount)
    if (!(th >= 0)) return setError(t('阈值必须 ≥ 0'))
    if (!(dc > 0 && dc <= 1))
      return setError(t('折扣必须在 (0, 1] 区间，如 0.92 表示 92 折'))

    const payloads: TierRuleInput[] = []
    for (const uid of uidList) {
      for (const model of modelList) {
        payloads.push({
          user_id: uid,
          channel_id: ch,
          model,
          threshold_rmb: th,
          discount: dc,
          enabled,
          remark: remark.trim(),
        })
      }
    }

    setSaving(true)
    try {
      const result = await onSubmit(payloads)
      const summary: string[] = []
      if (result.created) summary.push(t('新建 {{n}} 条', { n: result.created }))
      if (result.skipped)
        summary.push(t('跳过已存在 {{n}} 条', { n: result.skipped }))
      if (result.failed.length)
        summary.push(t('失败 {{n}} 条', { n: result.failed.length }))

      if (result.failed.length) {
        const detail = result.failed.slice(0, 3).join('；')
        setError(`${summary.join('，')}：${detail}`)
        toast.warning(summary.join('，'))
      } else {
        toast.success(summary.join('，') || t('Saved'))
        onOpenChange(false)
      }
    } catch (e) {
      setError((e as Error).message || t('保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const fieldCls = 'space-y-2'
  const hintCls = 'text-muted-foreground text-xs'

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className='w-[460px] sm:max-w-[460px]'>
        <SheetHeader>
          <SheetTitle>{isEditing ? t('编辑档位') : t('新增档位')}</SheetTitle>
          <SheetDescription>
            {isEditing
              ? t('用户 / 渠道 / 模型 为身份维度，编辑时不可修改；可调整阈值、折扣与启停')
              : t(
                  '同一用户+渠道+模型可配多条阈值构成阶梯；可一次为多个用户、多个模型批量创建'
                )}
          </SheetDescription>
        </SheetHeader>
        <div className='space-y-4 overflow-y-auto px-4'>
          <div className={fieldCls}>
            <Label htmlFor='td-user'>{t('用户')}</Label>
            <MultiSelect
              id='td-user'
              options={userOptions}
              selected={userIds}
              onChange={setUserIds}
              placeholder={
                optionsLoading ? t('加载中...') : t('搜索并选择用户（可多选）')
              }
              emptyText={t('无匹配用户')}
              maxVisibleChips={3}
              disabled={isEditing}
            />
            <p className={hintCls}>
              {isEditing
                ? t('身份维度不可修改')
                : t('多选后按「用户 × 模型」批量生成档位')}
            </p>
          </div>

          <div className={fieldCls}>
            <Label htmlFor='td-channel'>{t('渠道')}</Label>
            <Select
              items={channelOptions}
              value={channelId}
              onValueChange={applyChannel}
              disabled={isEditing}
            >
              <SelectTrigger id='td-channel' className='w-full'>
                <SelectValue
                  placeholder={
                    optionsLoading ? t('加载中...') : t('选择渠道（单选）')
                  }
                />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {channelOptions.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      {c.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <p className={hintCls}>
              {t('渠道删除重建后 ID 会变，规则需重新配置')}
            </p>
          </div>

          <div className={fieldCls}>
            <Label htmlFor='td-model'>{t('模型')}</Label>
            <MultiSelect
              id='td-model'
              options={modelOptions}
              selected={models}
              onChange={setModels}
              allowCreate
              placeholder={
                channelId
                  ? t('选择该渠道下的模型（可多选 / 可手输）')
                  : t('请先选择渠道')
              }
              emptyText={t('该渠道未配置模型，可直接输入模型名')}
              createLabel='添加 "{{value}}"'
              maxVisibleChips={3}
              disabled={isEditing}
            />
            <p className={hintCls}>
              {channelId
                ? selectedChannel
                  ? t('来源：渠道「{{name}}」已配置模型，共 {{n}} 个', {
                      name: selectedChannel.name,
                      n: modelOptions.length,
                    })
                  : ''
                : t('模型选项跟随渠道联动')}
            </p>
          </div>

          <div className={fieldCls}>
            <Label htmlFor='td-threshold'>{t('累计实耗达到（元）')}</Label>
            <Input
              id='td-threshold'
              type='number'
              min={0}
              placeholder='如 100 表示累计实付满 100 元'
              value={threshold}
              onChange={(e) => setThreshold(e.target.value)}
            />
            <p className={hintCls}>
              0 表示起步档（首笔即按此折扣）；按净额（消费−退款）累计
            </p>
          </div>

          <div className={fieldCls}>
            <Label htmlFor='td-discount'>{t('折扣')}</Label>
            <Input
              id='td-discount'
              type='number'
              step='0.01'
              min={0.01}
              max={1}
              placeholder='0.92 表示 92 折'
              value={discount}
              onChange={(e) => setDiscount(e.target.value)}
            />
          </div>

          <div className={fieldCls}>
            <Label htmlFor='td-remark'>{t('备注')}</Label>
            <Input
              id='td-remark'
              placeholder='方便记这是给谁开的'
              value={remark}
              onChange={(e) => setRemark(e.target.value)}
            />
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
          <Button onClick={submit} disabled={saving || optionsLoading}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
