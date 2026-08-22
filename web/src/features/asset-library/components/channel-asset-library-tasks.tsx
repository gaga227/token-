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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ListChecks, Loader2, RotateCcw } from 'lucide-react'
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

import {
  listChannelAssetLibraryTasks,
  retryChannelAssetLibraryTask,
  type AssetLibraryTask,
} from '../api'
import { assetLibraryQueryKeys, getAssetLibraryErrorMessage } from '../lib'

const TASK_TYPE_LABELS: Record<string, string> = {
  sync_channel: 'Sync channel',
  replicate_group: 'Replicate group',
  replicate_asset: 'Replicate asset',
  delete_asset_replicas: 'Delete asset replicas',
  delete_group_replicas: 'Delete group replicas',
}

const STATE_TONES: Record<
  AssetLibraryTask['state'],
  ComponentProps<typeof Badge>['variant']
> = {
  pending: 'secondary',
  running: 'default',
  done: 'outline',
  failed: 'destructive',
}

export function ChannelAssetLibraryTasks(props: {
  channelId: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const queryKey = assetLibraryQueryKeys.channelTasks(props.channelId)
  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () =>
      listChannelAssetLibraryTasks(props.channelId, { pageSize: 10 }),
  })
  const retryMutation = useMutation({
    mutationFn: (taskId: number) =>
      retryChannelAssetLibraryTask(props.channelId, taskId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey })
      toast.success(t('Asset library task retry scheduled'))
    },
    onError: (error) =>
      toast.error(
        getAssetLibraryErrorMessage(
          error,
          t('Failed to retry asset library task')
        )
      ),
  })

  const tasks = data?.items ?? []

  let content
  if (isLoading) {
    content = (
      <div className='text-muted-foreground flex items-center gap-2 py-4 text-sm'>
        <Loader2 className='size-4 animate-spin' />
        {t('Loading asset library tasks...')}
      </div>
    )
  } else if (tasks.length === 0) {
    content = (
      <p className='text-muted-foreground py-4 text-sm'>
        {t('No asset library tasks yet.')}
      </p>
    )
  } else {
    content = (
      <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Task')}</TableHead>
              <TableHead>{t('State')}</TableHead>
              <TableHead>{t('Attempts')}</TableHead>
              <TableHead>{t('Last run')}</TableHead>
              <TableHead className='w-16' />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tasks.map((task) => (
              <TableRow key={task.id}>
                <TableCell>
                  <div className='flex flex-col'>
                    <span>
                      {t(TASK_TYPE_LABELS[task.task_type] ?? task.task_type)}
                    </span>
                    {task.target_id && (
                      <span className='text-muted-foreground font-mono text-xs'>
                        {task.target_id}
                      </span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant={STATE_TONES[task.state] ?? 'secondary'}>
                    {t(task.state)}
                  </Badge>
                </TableCell>
                <TableCell>
                  {task.attempts}/{task.max_attempts}
                </TableCell>
                <TableCell>
                  <Tooltip>
                    <TooltipTrigger>
                      <span className='text-muted-foreground cursor-default text-xs'>
                        {new Date(task.updated_time * 1000).toLocaleString()}
                      </span>
                    </TooltipTrigger>
                    {task.last_error && (
                      <TooltipContent className='max-w-xs break-all'>
                        {task.last_error}
                      </TooltipContent>
                    )}
                  </Tooltip>
                </TableCell>
                <TableCell>
                  {task.state === 'failed' && (
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon-sm'
                      disabled={props.disabled || retryMutation.isPending}
                      onClick={() => retryMutation.mutate(task.id)}
                    >
                      <RotateCcw />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
    )
  }

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Background tasks')}
        description={t(
          'Replication and deletion jobs for this channel. Failed jobs are retried automatically.'
        )}
        icon={<ListChecks className='h-4 w-4' aria-hidden='true' />}
      />
      {content}
    </SideDrawerSection>
  )
}
