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
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import type { AssetReplicaChannel, AssetReplicaSummary } from '../types'

function getReplicationVariant(
  replication?: AssetReplicaSummary
): StatusVariant {
  if (!replication || replication.Total === 0) return 'neutral'
  if (replication.Failed > 0) return 'danger'
  if (replication.Processing > 0 || replication.Ready < replication.Total) {
    return 'warning'
  }
  return 'success'
}

function getChannelVariant(state: string): StatusVariant {
  switch (state) {
    case 'ready':
      return 'success'
    case 'failed':
      return 'danger'
    case 'processing':
      return 'warning'
    default:
      return 'neutral'
  }
}

function getChannelStatusLabel(
  t: (key: string) => string,
  state: string
): string {
  switch (state) {
    case 'ready':
      return t('Synced')
    case 'failed':
      return t('Sync failed')
    case 'processing':
      return t('Syncing')
    default:
      return t('Not synced')
  }
}

function ChannelBadge(props: { channel: AssetReplicaChannel }) {
  const { t } = useTranslation()
  const channel = props.channel
  const name = channel.Name || `#${channel.ChannelId}`
  const statusLabel = getChannelStatusLabel(t, channel.State)

  const badge = (
    <StatusBadge
      label={`${name} · ${statusLabel}`}
      variant={getChannelVariant(channel.State)}
      copyable={false}
      pulse={channel.State === 'processing'}
    />
  )

  if (!channel.LastError) {
    return badge
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={<span className='inline-flex max-w-full' tabIndex={0} />}
      >
        {badge}
      </TooltipTrigger>
      <TooltipContent>{channel.LastError}</TooltipContent>
    </Tooltip>
  )
}

export function ReplicationBadge(props: { replication?: AssetReplicaSummary }) {
  const { t } = useTranslation()
  const replication = props.replication
  if (!replication) return null

  if (replication.Channels && replication.Channels.length > 0) {
    return (
      <span className='flex max-w-full flex-wrap gap-1'>
        {replication.Channels.map((channel) => (
          <ChannelBadge key={channel.ChannelId} channel={channel} />
        ))}
      </span>
    )
  }

  const ready = replication.Ready
  const total = replication.Total
  let label = t('Not synchronized')
  if (total > 0) {
    label = t('{{ready}} of {{total}} channels ready', { ready, total })
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span className='inline-flex max-w-full' tabIndex={0} role='status' />
        }
      >
        <StatusBadge
          label={label}
          variant={getReplicationVariant(replication)}
          copyable={false}
          className='-ml-1.5'
        />
      </TooltipTrigger>
      <TooltipContent>
        {t('Ready: {{ready}}, processing: {{processing}}, failed: {{failed}}', {
          ready,
          processing: replication.Processing,
          failed: replication.Failed,
        })}
      </TooltipContent>
    </Tooltip>
  )
}
