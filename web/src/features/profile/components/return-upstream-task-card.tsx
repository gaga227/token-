/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ArrowUpRight } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CardContent } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { TitledCard } from '@/components/ui/titled-card'

import { updateUserSettings } from '../api'
import { parseUserSettings } from '../lib'
import type { UserProfile } from '../types'

// ============================================================================
// Return Upstream Task ID Card
// 独立醒目的开关卡片：创建异步任务时是否返回上游原始 task id（如 cgt-xxx）。
// 需要渠道侧开关同时开启才生效。开关拨动即保存。
// ============================================================================

interface ReturnUpstreamTaskCardProps {
  profile: UserProfile | null
  onUpdate: () => void
}

export function ReturnUpstreamTaskCard({
  profile,
  onUpdate,
}: ReturnUpstreamTaskCardProps) {
  const { t } = useTranslation()
  const [checked, setChecked] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (profile?.setting) {
      setChecked(!!parseUserSettings(profile.setting).return_upstream_task_id)
    }
  }, [profile])

  const toggle = async (value: boolean) => {
    const prev = checked
    setChecked(value)
    setSaving(true)
    try {
      const base = profile?.setting ? parseUserSettings(profile.setting) : {}
      const resp = await updateUserSettings({
        ...base,
        return_upstream_task_id: value,
      })
      if (resp.success) {
        toast.success(t('Settings updated successfully'))
        onUpdate()
      } else {
        setChecked(prev)
        toast.error(resp.message || t('Failed to update settings'))
      }
    } catch {
      setChecked(prev)
      toast.error(t('Failed to update settings'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <TitledCard
      title={t('Return upstream task ID')}
      description={t(
        'Return the upstream original task id (e.g. cgt-xxx) when creating async tasks. Takes effect only when the channel-side switch is also enabled'
      )}
      icon={<ArrowUpRight className='h-4 w-4' />}
      iconTone='info'
      disableHoverEffect
    >
      <CardContent className='!pt-0'>
        <div className='flex items-center justify-between gap-3 rounded-lg border bg-muted/40 p-3 sm:p-4'>
          <div className='space-y-0.5'>
            <Label htmlFor='returnUpstreamTaskId'>
              {t('Return upstream task ID')}
            </Label>
            <p className='text-muted-foreground text-xs sm:text-sm'>
              {t(
                'Creating async tasks will return the upstream original id (e.g. cgt-xxx) in the response'
              )}
            </p>
          </div>
          <Switch
            id='returnUpstreamTaskId'
            disabled={saving}
            checked={checked}
            onCheckedChange={toggle}
          />
        </div>
      </CardContent>
    </TitledCard>
  )
}
