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
import {
  Clapperboard,
  Download,
  Film,
  Play,
  RefreshCw,
  Upload,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'

import {
  submitVideoTask,
  getVideoTask,
  getVideoModels,
  getUserGroups,
  fetchVideoTaskHistory,
  uploadAssetFile,
} from './api'
import {
  DEFAULT_DURATION,
  DEFAULT_SIZE,
  MINIMAX_PRESET,
  POLL_INTERVAL_MS,
  POLL_TIMEOUT_MS,
  SEEDANCE_PRESET,
  SEEDANCE_PRICE_PER_SECOND,
  TERMINAL_STATUSES,
  WAN_PRESET,
  WAN_PRICE_PER_SECOND,
  WAN_SMART_DURATION,
  minimaxSizeOptions,
  videoModelFamily,
} from './constants'
import {
  loadVideoHistory,
  mergeServerHistory,
  saveVideoHistory,
} from './storage'
import type {
  GroupOption,
  VideoModelFamily,
  VideoSubmitRequest,
  VideoTaskRecord,
} from './types'

const BASE_PRICE_PER_SECOND = 0.5

function sizeRatio(size: string): number {
  const longEdge = Math.max(
    ...size.split('x').map((v) => Number.parseInt(v, 10) || 0)
  )
  return longEdge >= 1792 ? 1.6 : 1.0
}

function estimateCost(
  family: VideoModelFamily,
  duration: number,
  size: string,
  resolution: string,
  wanPerSecond?: number
): number | null {
  if (family === 'wan') {
    if (wanPerSecond === undefined) return null // unpriced wan models settle on usage
    // Smart duration (-1) is pre-charged at the 30s cap, settled on output.
    const seconds = duration === WAN_SMART_DURATION ? 30 : duration
    return Math.round(seconds * wanPerSecond * 100) / 100
  }
  if (family === 'seedance') {
    const perSecond = SEEDANCE_PRICE_PER_SECOND[resolution] ?? 1.09
    return Math.round(duration * perSecond * 100) / 100
  }
  return Math.round(duration * sizeRatio(size) * BASE_PRICE_PER_SECOND * 100) / 100
}

function formatTimestamp(seconds?: number): string {
  if (!seconds) return '-'
  return new Date(seconds * 1000).toLocaleString()
}

function statusBadgeVariant(status: string): {
  variant: 'default' | 'secondary' | 'destructive' | 'warning' | 'outline'
} {
  switch (status) {
    case 'completed':
      return { variant: 'default' }
    case 'failed':
    case 'cancelled':
      return { variant: 'destructive' }
    case 'processing':
    case 'in_progress':
      return { variant: 'warning' }
    case 'queued':
    case 'unknown':
    case 'not_started':
      return { variant: 'secondary' }
    default:
      return { variant: 'outline' }
  }
}

// Display labels for upstream task statuses (i18n keys).
const STATUS_LABELS: Record<string, string> = {
  queued: 'Queued',
  unknown: 'Unknown',
  not_started: 'Not started',
  processing: 'Processing',
  in_progress: 'Processing',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status
}

export function VideoGeneration() {
  const { t } = useTranslation()

  // ---- options ----
  const [models, setModels] = useState<string[]>([])
  const [groups, setGroups] = useState<GroupOption[]>([])
  const [model, setModel] = useState('')
  const [group, setGroup] = useState('default')

  // ---- form ----
  const [prompt, setPrompt] = useState('')
  const [duration, setDuration] = useState<number>(DEFAULT_DURATION)
  const [size, setSize] = useState<string>(DEFAULT_SIZE)
  // Seedance-family selectors (submitted via metadata {resolution, ratio}).
  const [resolution, setResolution] = useState<string>(
    SEEDANCE_PRESET.defaultResolution
  )
  const [ratio, setRatio] = useState<string>(SEEDANCE_PRESET.defaultRatio)
  // Wan-family selectors: the resolution tier goes through `size` while the
  // ratio rides on metadata.parameters.ratio (gateway merges it upstream).
  const [wanResolution, setWanResolution] = useState<string>(
    WAN_PRESET.defaultResolution
  )
  const [wanRatio, setWanRatio] = useState<string>(WAN_PRESET.defaultRatio)
  const [imageUrl, setImageUrl] = useState('')
  const [referenceVideos, setReferenceVideos] = useState('')
  // Which upload target is currently in flight (null = idle).
  const [uploading, setUploading] = useState<'image' | 'videos' | null>(null)
  const imageInputRef = useRef<HTMLInputElement>(null)
  const videoInputRef = useRef<HTMLInputElement>(null)

  // ---- tasks ----
  // History is persisted to localStorage (storage.ts) and restored on mount
  // so the list survives page reloads. The main card always shows history[0]
  // (the newest record); older records render below under "History".
  const [history, setHistory] = useState<VideoTaskRecord[]>(() =>
    loadVideoHistory()
  )
  const [submitting, setSubmitting] = useState(false)

  const tasksRef = useRef(new Map<string, VideoTaskRecord>())
  const pollTimersRef = useRef(
    new Map<string, ReturnType<typeof setInterval>>()
  )
  // Latest history snapshot — async server sync (below) reads it to merge
  // without stale closures.
  const historyRef = useRef(history)
  useEffect(() => {
    historyRef.current = history
  }, [history])

  // Mirror history into localStorage on every change (submit / poll / refresh).
  useEffect(() => {
    saveVideoHistory(history)
  }, [history])

  useEffect(() => {
    getVideoModels()
      .then((list) => {
        setModels(list)
        setModel((prev) => (list.includes(prev) ? prev : (list[0] ?? '')))
      })
      .catch(() => toast.error(t('Failed to load video models')))
  }, [t])

  useEffect(() => {
    getUserGroups()
      .then((list) => {
        setGroups(list)
        setGroup((prev) =>
          list.some((g) => g.value === prev)
            ? prev
            : (list[0]?.value ?? 'default')
        )
      })
      .catch(() => {
        // Group loading is optional — fall back to the default group.
      })
  }, [])

  // Selected model's upstream family — drives the duration / resolution /
  // aspect-ratio option sets below.
  const family = useMemo(() => videoModelFamily(model), [model])
  const isSeedance = family === 'seedance'
  const isWan = family === 'wan'
  // MiniMax family pixel-size options — flat-H3 variants (minimax-h3-*) are
  // restricted to 768P; capitalised MiniMax-H3 keeps the 2K tier too.
  const minimaxSizes = useMemo(() => minimaxSizeOptions(model), [model])
  // Wan family per-second price for the selected resolution (undefined for
  // unpriced wan models — the estimate then falls back to usage-based).
  const wanPerSecond = useMemo(
    () => WAN_PRICE_PER_SECOND[model]?.[wanResolution],
    [model, wanResolution]
  )
  // prime bills by input-video duration once a reference video is attached.
  const isWanPrime = model === 'wan3.0-video-prime'

  // Re-fit the form options whenever the family / model changes: durations
  // and size/resolution/ratio snap to the supported values.
  useEffect(() => {
    if (isSeedance) {
      setDuration((prev) =>
        (SEEDANCE_PRESET.durations as readonly number[]).includes(prev)
          ? prev
          : SEEDANCE_PRESET.defaultDuration
      )
      setResolution((prev) =>
        (SEEDANCE_PRESET.resolutions as readonly string[]).includes(prev)
          ? prev
          : SEEDANCE_PRESET.defaultResolution
      )
      setRatio((prev) =>
        (SEEDANCE_PRESET.ratios as readonly string[]).includes(prev)
          ? prev
          : SEEDANCE_PRESET.defaultRatio
      )
    } else if (isWan) {
      setDuration((prev) =>
        (WAN_PRESET.durations as readonly number[]).includes(prev) ||
        prev === WAN_SMART_DURATION
          ? prev
          : WAN_PRESET.defaultDuration
      )
      setWanResolution((prev) =>
        (WAN_PRESET.resolutions as readonly string[]).includes(prev)
          ? prev
          : WAN_PRESET.defaultResolution
      )
      setWanRatio((prev) =>
        (WAN_PRESET.ratios as readonly string[]).includes(prev)
          ? prev
          : WAN_PRESET.defaultRatio
      )
    } else {
      setDuration((prev) =>
        (MINIMAX_PRESET.durations as readonly number[]).includes(prev)
          ? prev
          : MINIMAX_PRESET.defaultDuration
      )
      setSize((prev) =>
        minimaxSizes.some((s) => s.value === prev)
          ? prev
          : (minimaxSizes[0]?.value ?? MINIMAX_PRESET.defaultSize)
      )
    }
  }, [isSeedance, isWan, minimaxSizes])

  // Insert a record on top when it is new; update it in place when it already
  // exists (so background polls of older tasks never hijack the main card).
  const upsertTask = useCallback((record: VideoTaskRecord) => {
    tasksRef.current.set(record.taskId, record)
    setHistory((prev) => {
      const exists = prev.some((item) => item.taskId === record.taskId)
      if (exists) {
        return prev.map((item) =>
          item.taskId === record.taskId ? record : item
        )
      }
      return [record, ...prev]
    })
  }, [])

  const stopPolling = useCallback((taskId: string) => {
    const timer = pollTimersRef.current.get(taskId)
    if (timer) clearInterval(timer)
    pollTimersRef.current.delete(taskId)
  }, [])

  /**
   * Query a task once and merge the fresh status/progress/url into history.
   * Returns the resulting status, or null when the task is not known.
   * `fallback` supplies the record for tasks restored from localStorage that
   * have not been upserted yet (so their poll/refresh still works).
   * Throws on transient network errors (caller decides whether to retry).
   */
  const resolveTaskOnce = useCallback(
    async (
      taskId: string,
      fallback?: VideoTaskRecord
    ): Promise<string | null> => {
      const prev = tasksRef.current.get(taskId) ?? fallback
      if (!prev) return null
      const resp = await getVideoTask(taskId)
      if (!resp) {
        upsertTask({
          ...prev,
          status: 'failed',
          errorMessage: t('Task not found'),
        })
        return 'failed'
      }
      const cur = tasksRef.current.get(taskId) ?? prev
      const record: VideoTaskRecord = {
        ...cur,
        status: resp.status || cur.status,
        progress:
          resp.status === 'completed'
            ? 100
            : (resp.progress ?? cur.progress),
        videoUrl: resp.metadata?.url || cur.videoUrl,
        errorMessage:
          resp.status === 'failed'
            ? (resp.error?.message || 'task failed')
            : cur.errorMessage,
        consumedInput: resp.consumed_input_amount,
        consumedOutput: resp.consumed_output_amount,
      }
      upsertTask(record)
      return record.status
    },
    [t, upsertTask]
  )

  // Poll one task until it reaches a terminal state or POLL_TIMEOUT_MS
  // elapses. Multiple tasks can be polled concurrently (each has its own
  // interval); restarting polling for the same task is a no-op. `record` is
  // the source record (e.g. from a restored history) when the task has not
  // been upserted yet.
  const startPolling = useCallback(
    (taskId: string, record?: VideoTaskRecord) => {
      if (pollTimersRef.current.has(taskId)) return
      if (record && !tasksRef.current.has(taskId)) {
        tasksRef.current.set(taskId, record)
      }
      const startedAt = Date.now()
      const tick = async () => {
        const prev = tasksRef.current.get(taskId)
        if (!prev) return
        if (Date.now() - startedAt > POLL_TIMEOUT_MS) {
          stopPolling(taskId)
          upsertTask({
            ...tasksRef.current.get(taskId)!,
            status: 'failed',
            errorMessage: t('Task polling timed out'),
          })
          return
        }
        if (TERMINAL_STATUSES.has(prev.status)) {
          stopPolling(taskId)
          return
        }
        try {
          const status = await resolveTaskOnce(taskId, prev)
          if (status && TERMINAL_STATUSES.has(status)) stopPolling(taskId)
        } catch {
          // Transient network error — the next tick keeps polling.
        }
      }
      pollTimersRef.current.set(
        taskId,
        setInterval(tick, POLL_INTERVAL_MS)
      )
      // Probe immediately so a page restored after reload refreshes fast.
      void tick()
    },
    [resolveTaskOnce, stopPolling, t, upsertTask]
  )

  // Resume polling for tasks that were still running when the page was
  // closed/reloaded. `history` here is the snapshot restored from storage.
  useEffect(() => {
    for (const record of history) {
      if (!TERMINAL_STATUSES.has(record.status)) {
        startPolling(record.taskId, record)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Sync the server-side task history once on mount: tasks submitted from
  // another browser/device (or trimmed out of localStorage) are pulled back
  // in, and any running ones resume polling. The server is authoritative for
  // status/progress/url; submission-only fields (prompt/duration/size) are
  // kept from the local snapshot when present.
  useEffect(() => {
    let cancelled = false
    fetchVideoTaskHistory()
      .then((serverItems) => {
        if (cancelled || serverItems.length === 0) return
        const base = historyRef.current
        const merged = mergeServerHistory(base, serverItems)
        historyRef.current = merged
        setHistory(merged)
        const knownIds = new Set(base.map((r) => r.taskId))
        for (const record of merged) {
          if (
            !knownIds.has(record.taskId) &&
            !TERMINAL_STATUSES.has(record.status)
          ) {
            startPolling(record.taskId, record)
          }
        }
      })
      .catch(() => {
        // Best-effort sync — the localStorage snapshot is already restored
        // and polling resumed from it. Nothing user-visible to report.
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Clear every poll timer when the page unmounts.
  useEffect(() => {
    const timers = pollTimersRef.current
    return () => {
      timers.forEach((timer) => clearInterval(timer))
      timers.clear()
    }
  }, [])

  // Manually re-query one history card (refreshes an expired video URL or
  // a stale status) and take over polling again while it is still running.
  const refreshTask = useCallback(
    async (taskId: string, record: VideoTaskRecord) => {
      try {
        const status = await resolveTaskOnce(taskId, record)
        if (status && !TERMINAL_STATUSES.has(status)) {
          startPolling(taskId, record)
        }
      } catch {
        toast.error(t('Refresh failed'))
      }
    },
    [resolveTaskOnce, startPolling, t]
  )

  // Reference-video upload cap: wan3.0 upstream allows 5, others 3.
  const maxReferenceVideos = isWan ? WAN_PRESET.maxReferenceVideos : 3

  const referenceVideoList = useMemo(
    () =>
      referenceVideos
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean)
        .slice(0, maxReferenceVideos),
    [referenceVideos, maxReferenceVideos]
  )

  const handleSubmit = async () => {
    if (!model) {
      toast.error(t('Please select a model'))
      return
    }
    if (!prompt.trim()) {
      toast.error(t('Please enter a prompt'))
      return
    }
    setSubmitting(true)
    try {
      const payload: VideoSubmitRequest = {
        model,
        prompt: prompt.trim(),
        group: group || undefined,
      }
      if (isWan) {
        // Wan family: resolution tier through `size`; ratio (and smart
        // duration -1) through metadata.parameters — the gateway merges
        // these into the upstream DashScope parameters object.
        payload.size = wanResolution
        const parameters: Record<string, unknown> = { ratio: wanRatio }
        if (duration === WAN_SMART_DURATION) {
          // Smart duration: top-level duration validation rejects -1, so it
          // rides on metadata only.
          parameters.duration = WAN_SMART_DURATION
        } else {
          payload.duration = duration
        }
        payload.metadata = { parameters }
      } else if (isSeedance) {
        // Seedance (doubao native contract): resolution + ratio go through
        // metadata; no pixel size field.
        payload.metadata = { resolution, ratio }
        payload.duration = duration
      } else {
        payload.size = size
        payload.duration = duration
      }
      if (imageUrl.trim()) payload.image = imageUrl.trim()
      if (referenceVideoList.length > 0) {
        payload.reference_video_urls = referenceVideoList
      }
      const resp = await submitVideoTask(payload)
      const taskId = resp.id || resp.task_id
      if (!taskId) {
        throw new Error(t('Task not found'))
      }
      const record: VideoTaskRecord = {
        taskId,
        model,
        prompt: prompt.trim(),
        duration,
        size: isWan
          ? duration === WAN_SMART_DURATION
            ? `智能时长 · ${wanResolution} · ${wanRatio}`
            : `${wanResolution} · ${wanRatio}`
          : isSeedance
            ? `${resolution} · ${ratio}`
            : size,
        status: resp.status || 'queued',
        progress: resp.progress ?? 0,
        createdAt: resp.created_at || Math.floor(Date.now() / 1000),
        videoUrl: resp.metadata?.url,
        errorMessage: resp.error?.message,
      }
      upsertTask(record)
      startPolling(taskId, record)
      toast.success(t('Task submitted'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to submit video task')
      )
    } finally {
      setSubmitting(false)
    }
  }

  // Upload a local file to the gateway asset storage and return its URL.
  const uploadToAssetStorage = async (file: File): Promise<string> => {
    return uploadAssetFile(file)
  }

  const handleUploadImage = async (file: File | undefined) => {
    if (!file) return
    setUploading('image')
    try {
      const url = await uploadToAssetStorage(file)
      setImageUrl(url)
      toast.success(t('Upload succeeded'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Upload failed')
      )
    } finally {
      setUploading(null)
    }
  }

  const handleUploadVideos = async (files: FileList | null) => {
    if (!files || files.length === 0) return
    setUploading('videos')
    try {
      const existing = referenceVideoList
      const added: string[] = []
      for (const file of Array.from(files)) {
        if (existing.length + added.length >= maxReferenceVideos) break
        added.push(await uploadToAssetStorage(file))
      }
      if (added.length === 0) {
        toast.error(
          t('Reference videos are limited to {{n}}', {
            n: maxReferenceVideos,
          })
        )
        return
      }
      setReferenceVideos((prev) => {
        const lines = prev
          .split('\n')
          .map((l) => l.trim())
          .filter(Boolean)
        return [...lines, ...added].slice(0, maxReferenceVideos).join('\n')
      })
      toast.success(t('Upload succeeded'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Upload failed')
      )
    } finally {
      setUploading(null)
    }
  }

  const estimated = estimateCost(
    family,
    duration,
    size,
    resolution,
    wanPerSecond
  )

  const renderTaskCard = (record: VideoTaskRecord, detailed: boolean) => {
    const { variant } = statusBadgeVariant(record.status)
    return (
      <Card key={record.taskId}>
        <CardHeader className='pb-3'>
          <div className='flex items-center justify-between gap-2'>
            <CardTitle className='flex min-w-0 items-center gap-2 text-sm'>
              <Film className='text-muted-foreground size-4 shrink-0' />
              <span className='truncate font-mono text-xs' title={record.taskId}>
                {record.taskId}
              </span>
              {detailed && (
                <CopyButton
                  value={record.taskId}
                  className='size-6'
                  iconClassName='size-3.5'
                  tooltip={t('Copy')}
                  successTooltip={t('Copied')}
                  aria-label={t('Copy')}
                />
              )}
            </CardTitle>
            <div className='flex shrink-0 items-center gap-1'>
              {!detailed && (
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => refreshTask(record.taskId, record)}
                  title={t('Refresh')}
                  aria-label={t('Refresh')}
                >
                  <RefreshCw className='size-3.5' />
                </Button>
              )}
              <Badge variant={variant}>{t(statusLabel(record.status))}</Badge>
            </div>
          </div>
          <CardDescription className='line-clamp-2'>
            {record.prompt}
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-3'>
          <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
            <span>{record.model}</span>
            {record.duration > 0 && (
              <span>
                {record.duration}s{record.size ? ` · ${record.size}` : ''}
              </span>
            )}
            <span>{formatTimestamp(record.createdAt)}</span>
          </div>

          {record.status !== 'completed' && record.status !== 'failed' && (
            <div className='space-y-1.5'>
              <Progress value={record.progress} />
              <p className='text-muted-foreground text-xs'>
                {record.progress > 0 ? `${record.progress}%` : t('Queued')}
              </p>
            </div>
          )}

          {record.status === 'failed' && record.errorMessage && (
            <p className='text-destructive text-xs'>{record.errorMessage}</p>
          )}

          {record.videoUrl && (
            <div className='space-y-2'>
              <video
                src={record.videoUrl}
                controls
                preload='metadata'
                className='bg-black max-h-[60vh] w-full rounded-lg'
              />
              <div className='flex items-center gap-2'>
                <Button variant='outline' size='sm' render={
                  <a href={record.videoUrl} target='_blank' rel='noopener noreferrer' />
                }>
                  <Play />
                  {t('Open in new tab')}
                </Button>
                <Button variant='outline' size='sm' render={
                  <a href={record.videoUrl} download target='_blank' rel='noopener noreferrer' />
                }>
                  <Download />
                  {t('Download')}
                </Button>
              </div>
            </div>
          )}

          {detailed &&
            (record.consumedInput !== undefined ||
              record.consumedOutput !== undefined) && (
              <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
                <span>
                  {t('Input cost')}: ¥{(record.consumedInput ?? 0).toFixed(2)}
                </span>
                <span>
                  {t('Output cost')}: ¥{(record.consumedOutput ?? 0).toFixed(2)}
                </span>
              </div>
            )}
        </CardContent>
      </Card>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Video Generation')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto grid w-full max-w-6xl gap-4 lg:grid-cols-[420px_1fr]'>
          {/* ---- submission form ---- */}
          <div className='space-y-4'>
            <Card>
              <CardHeader className='pb-3'>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Clapperboard className='size-4' />
                  {t('Create Video Task')}
                </CardTitle>
              </CardHeader>
              <CardContent className='space-y-4'>
                <div className='grid grid-cols-2 gap-3'>
                  <div className='space-y-1.5'>
                    <Label>{t('Model')}</Label>
                    <Select value={model} onValueChange={(v) => setModel(v ?? '')}>
                      <SelectTrigger className='w-full'>
                        <SelectValue
                          placeholder={t('Select a model')}
                        />
                      </SelectTrigger>
                      <SelectContent>
                        {models.map((m) => (
                          <SelectItem key={m} value={m}>
                            {m}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className='space-y-1.5'>
                    <Label>{t('Group')}</Label>
                    <Select value={group} onValueChange={(v) => setGroup(v ?? '')}>
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Group')} />
                      </SelectTrigger>
                      <SelectContent>
                        {groups.map((g) => (
                          <SelectItem key={g.value} value={g.value}>
                            {g.value}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                <div className='space-y-1.5'>
                  <Label>{t('Prompt')}</Label>
                  <Textarea
                    value={prompt}
                    onChange={(e) => setPrompt(e.target.value)}
                    placeholder={t('Describe the video you want to generate')}
                    rows={4}
                  />
                </div>

                {isWan ? (
                  // Wan family (DashScope): duration (incl. smart), resolution
                  // tier via `size`, ratio via metadata.parameters.ratio.
                  <div className='grid grid-cols-3 gap-3'>
                    <div className='space-y-1.5'>
                      <Label>{t('Duration')}</Label>
                      <Select
                        value={String(duration)}
                        onValueChange={(v) => setDuration(Number(v))}
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value={String(WAN_SMART_DURATION)}>
                            {t('Smart (≤30s)')}
                          </SelectItem>
                          {WAN_PRESET.durations.map((d) => (
                            <SelectItem key={d} value={String(d)}>
                              {d}s
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-1.5'>
                      <Label>{t('Resolution')}</Label>
                      <Select
                        value={wanResolution}
                        onValueChange={(v) =>
                          setWanResolution(v ?? WAN_PRESET.defaultResolution)
                        }
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {WAN_PRESET.resolutions.map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-1.5'>
                      <Label>{t('Aspect ratio')}</Label>
                      <Select
                        value={wanRatio}
                        onValueChange={(v) =>
                          setWanRatio(v ?? WAN_PRESET.defaultRatio)
                        }
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {WAN_PRESET.ratios.map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                ) : isSeedance ? (
                  // Seedance family: native resolution + ratio enums.
                  <div className='grid grid-cols-3 gap-3'>
                    <div className='space-y-1.5'>
                      <Label>{t('Duration')}</Label>
                      <Select
                        value={String(duration)}
                        onValueChange={(v) => setDuration(Number(v))}
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {SEEDANCE_PRESET.durations.map((d) => (
                            <SelectItem key={d} value={String(d)}>
                              {d}s
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-1.5'>
                      <Label>{t('Resolution')}</Label>
                      <Select
                        value={resolution}
                        onValueChange={(v) =>
                          setResolution(v ?? SEEDANCE_PRESET.defaultResolution)
                        }
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {SEEDANCE_PRESET.resolutions.map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-1.5'>
                      <Label>{t('Aspect ratio')}</Label>
                      <Select
                        value={ratio}
                        onValueChange={(v) =>
                          setRatio(v ?? SEEDANCE_PRESET.defaultRatio)
                        }
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {SEEDANCE_PRESET.ratios.map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                ) : (
                  // MiniMax / generic family: OpenAI-style pixel sizes.
                  <div className='grid grid-cols-2 gap-3'>
                    <div className='space-y-1.5'>
                      <Label>{t('Duration')}</Label>
                      <Select
                        value={String(duration)}
                        onValueChange={(v) => setDuration(Number(v))}
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {MINIMAX_PRESET.durations.map((d) => (
                            <SelectItem key={d} value={String(d)}>
                              {d}s
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-1.5'>
                      <Label>{t('Resolution')}</Label>
                      <Select
                        value={size}
                        onValueChange={(v) => setSize(v ?? DEFAULT_SIZE)}
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {minimaxSizes.map((s) => (
                            <SelectItem key={s.value} value={s.value}>
                              {s.label}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                )}

                <div className='space-y-1.5'>
                  <div className='flex items-center justify-between'>
                    <Label>{t('First frame image URL (optional)')}</Label>
                    <Button
                      variant='ghost'
                      size='sm'
                      className='h-6 gap-1 px-2 text-xs'
                      disabled={uploading !== null}
                      onClick={() => imageInputRef.current?.click()}
                    >
                      {uploading === 'image' ? (
                        <Spinner className='size-3' />
                      ) : (
                        <Upload className='size-3' />
                      )}
                      {uploading === 'image' ? t('Uploading') : t('Upload')}
                    </Button>
                    <input
                      ref={imageInputRef}
                      type='file'
                      accept='image/*'
                      className='hidden'
                      onChange={(e) => {
                        void handleUploadImage(e.target.files?.[0])
                        e.target.value = ''
                      }}
                    />
                  </div>
                  <Input
                    value={imageUrl}
                    onChange={(e) => setImageUrl(e.target.value)}
                    placeholder='https://...'
                  />
                </div>

                <div className='space-y-1.5'>
                  <div className='flex items-center justify-between'>
                    <Label>
                      {t(
                        'Reference video URLs (optional, one per line, max {{n}})',
                        { n: maxReferenceVideos }
                      )}
                    </Label>
                    <Button
                      variant='ghost'
                      size='sm'
                      className='h-6 gap-1 px-2 text-xs'
                      disabled={uploading !== null}
                      onClick={() => videoInputRef.current?.click()}
                    >
                      {uploading === 'videos' ? (
                        <Spinner className='size-3' />
                      ) : (
                        <Upload className='size-3' />
                      )}
                      {uploading === 'videos' ? t('Uploading') : t('Upload')}
                    </Button>
                    <input
                      ref={videoInputRef}
                      type='file'
                      accept='video/*'
                      multiple
                      className='hidden'
                      onChange={(e) => {
                        void handleUploadVideos(e.target.files)
                        e.target.value = ''
                      }}
                    />
                  </div>
                  <Textarea
                    value={referenceVideos}
                    onChange={(e) => setReferenceVideos(e.target.value)}
                    placeholder={'https://example.com/ref-1.mp4\nhttps://example.com/ref-2.mp4'}
                    rows={2}
                  />
                </div>

                <div className='flex items-center justify-between gap-2 pt-1'>
                  <span className='text-muted-foreground text-xs'>
                    {estimated === null
                      ? t('Estimated cost') + ': ' + t('billed on actual usage')
                      : `${t('Estimated cost')}: ¥${estimated.toFixed(2)}`}
                  </span>
                  <Button onClick={handleSubmit} disabled={submitting}>
                    {submitting && <Spinner className='size-4' />}
                    {t('Generate Video')}
                  </Button>
                </div>
                {isWan && (
                  <p className='text-muted-foreground text-xs'>
                    {duration === WAN_SMART_DURATION
                      ? t(
                          'Smart duration is pre-charged at 30s and refunded by actual output length.'
                        )
                      : ''}
                    {isWanPrime &&
                      referenceVideoList.length > 0 &&
                      t(
                        'prime bills by the input video duration (settled on upstream usage).'
                      )}
                  </p>
                )}
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Material URLs must be publicly accessible (mainland China reachable). Billing is pre-charged on submission.'
                  )}
                </p>
              </CardContent>
            </Card>
          </div>

          {/* ---- task status & results ---- */}
          <div className='space-y-4'>
            {history[0] ? (
              renderTaskCard(history[0], true)
            ) : (
              <Card>
                <CardContent className='text-muted-foreground flex h-40 items-center justify-center text-sm'>
                  {t('Submit a task to see its status here')}
                </CardContent>
              </Card>
            )}

            {history.length > 1 && (
              <div className='space-y-2'>
                <h3 className='text-muted-foreground text-sm font-medium'>
                  {t('History')}
                </h3>
                <div className='space-y-3'>
                  {history
                    .slice(1)
                    .map((record) => renderTaskCard(record, false))}
                </div>
              </div>
            )}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
