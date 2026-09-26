/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import dayjs from '@/lib/dayjs'
import { CalendarDays, ChevronDown } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DatePicker } from '@/components/date-picker'
import { MultiSelect, type Option } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import type { ReportFilterParams } from '../api'

interface ReportFilterBarProps {  filters: ReportFilterParams
  onChange: (next: ReportFilterParams) => void
  userOptions: Option[]
  channelOptions: Option[]
  modelOptions: Option[]
  loading?: boolean
  onExport?: () => void
  exporting?: boolean
  /** 任务态按钮文案（排队中 / 已导出 x 行…） */
  exportLabel?: string
  /** 明细页隐藏「含失败请求」开关（汇总恒为净额） */
  showIncludeErrors?: boolean
}

export { filterToQuery as filtersToApiQuery } from '../api'

export function ReportFilterBar({
  filters,
  onChange,
  userOptions,
  channelOptions,
  modelOptions,
  loading,
  onExport,
  exporting,
  exportLabel,
  showIncludeErrors = true,
}: ReportFilterBarProps) {
  const { t } = useTranslation()
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const setDay = (which: 'start' | 'end', d: Date | undefined) => {
    if (which === 'start') {
      onChange({ ...filters, startDate: d })
    } else {
      onChange({ ...filters, endDate: d })
    }
  }

  const quickRange = (key: 'today' | 'yesterday' | '7d' | 'month' | 'lastMonth') => {
    const now = dayjs()
    let start = now.startOf('day')
    let end = now.startOf('day')
    if (key === 'today') {
      end = start
    } else if (key === 'yesterday') {
      start = start.subtract(1, 'day')
      end = start
    } else if (key === '7d') {
      start = start.subtract(6, 'day')
      end = now.startOf('day')
    } else if (key === 'month') {
      start = now.startOf('month')
      end = now.startOf('day')
    } else {
      start = now.subtract(1, 'month').startOf('month')
      end = start.endOf('month').startOf('day')
    }
    onChange({
      ...filters,
      startDate: start.toDate(),
      endDate: end.toDate(),
    })
  }

  const quickBtn = (key: 'today' | 'yesterday' | '7d' | 'month' | 'lastMonth', label: string) => (
    <Button
      key={key}
      variant='outline'
      size='sm'
      onClick={() => quickRange(key)}
      disabled={loading}
    >
      {label}
    </Button>
  )

  return (
    <div className='space-y-3'>
      <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4'>
        <div className='space-y-1.5'>
          <Label className='text-muted-foreground text-xs'>
            {t('用户')}（{t('可多选')}）
          </Label>
          <MultiSelect
            options={userOptions}
            selected={filters.userIds}
            onChange={(vals) => onChange({ ...filters, userIds: vals })}
            placeholder={t('全部用户')}
            maxVisibleChips={2}
            disabled={loading}
          />
        </div>
        <div className='space-y-1.5'>
          <Label className='text-muted-foreground text-xs'>
            {t('渠道')}（{t('可多选')}）
          </Label>
          <MultiSelect
            options={channelOptions}
            selected={filters.channelIds}
            onChange={(vals) => onChange({ ...filters, channelIds: vals })}
            placeholder={t('全部渠道')}
            maxVisibleChips={2}
            disabled={loading}
          />
        </div>
        <div className='space-y-1.5'>
          <Label className='text-muted-foreground text-xs'>
            {t('模型')}（{t('可多选')}）
          </Label>
          <MultiSelect
            options={modelOptions}
            selected={filters.models}
            onChange={(vals) => onChange({ ...filters, models: vals })}
            placeholder={t('全部模型')}
            maxVisibleChips={2}
            disabled={loading}
          />
        </div>
        <div className='space-y-1.5'>
          <Label className='text-muted-foreground text-xs'>
            {t('日期范围')}（{t('北京时间')}）
          </Label>
          <div className='flex items-center gap-2'>
            <DatePicker
              selected={filters.startDate}
              onSelect={(d) => setDay('start', d)}
              placeholder={t('开始日期')}
            />
            <span className='text-muted-foreground'>~</span>
            <DatePicker
              selected={filters.endDate}
              onSelect={(d) => setDay('end', d)}
              placeholder={t('结束日期')}
            />
          </div>
        </div>
      </div>

      <div className='flex flex-wrap items-center gap-2'>
        <CalendarDays className='text-muted-foreground h-4 w-4' />
        {quickBtn('today', t('今天'))}
        {quickBtn('yesterday', t('昨天'))}
        {quickBtn('7d', t('近7天'))}
        {quickBtn('month', t('本月'))}
        {quickBtn('lastMonth', t('上月'))}
        <div className='flex-1' />
        <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
          <CollapsibleTrigger asChild>
            <Button variant='ghost' size='sm'>
              {t('高级筛选')}
              <ChevronDown
                className={`h-4 w-4 transition-transform ${advancedOpen ? 'rotate-180' : ''}`}
              />
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className='mt-3 grid grid-cols-1 items-end gap-3 md:grid-cols-3'>
              <div className='space-y-1.5'>
                <Label className='text-muted-foreground text-xs'>
                  {t('分组')}
                </Label>
                <Input
                  value={filters.group}
                  onChange={(e) =>
                    onChange({ ...filters, group: e.target.value })
                  }
                  placeholder={t('全部分组')}
                />
              </div>
              <div className='space-y-1.5'>
                <Label className='text-muted-foreground text-xs'>
                  {t('令牌名称')}
                </Label>
                <Input
                  value={filters.tokenName}
                  onChange={(e) =>
                    onChange({ ...filters, tokenName: e.target.value })
                  }
                  placeholder={t('全部令牌')}
                />
              </div>
              {showIncludeErrors && (
                <div className='flex h-9 items-center gap-2'>
                  <Switch
                    id='include-errors'
                    checked={filters.includeErrors}
                    onCheckedChange={(v) =>
                      onChange({ ...filters, includeErrors: v })
                    }
                  />
                  <Label htmlFor='include-errors' className='text-sm'>
                    {t('包含失败请求')}
                  </Label>
                </div>
              )}
            </div>
          </CollapsibleContent>
        </Collapsible>
        {onExport && (
          <Button
            variant='outline'
            size='sm'
            onClick={onExport}
            disabled={exporting}
            title={exportLabel}
          >
            {exportLabel || t('导出 CSV')}
          </Button>
        )}
      </div>
    </div>
  )
}
