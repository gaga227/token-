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
*/

import { DEFAULT_SIZE } from './constants'
import type { VideoHistoryItem, VideoTaskRecord } from './types'

/**
 * Browser-side persistence for the video generation page history.
 *
 * Video tasks are async and take minutes to finish, but the page used to
 * keep its history only in component state — a refresh wiped everything.
 * We now mirror every task record into localStorage so the list survives
 * reloads, and unfinished tasks resume polling on mount.
 *
 * The stored copy is a plain snapshot (records are plain JSON). No task
 * content is fetched from the server on restore: status/progress/videoUrl
 * are whatever was last written, then refreshed by polling when needed.
 */
const STORAGE_KEY = 'video-generation-history-v1'

/** Keep at most this many records to bound storage usage (~KB range). */
const MAX_STORED_RECORDS = 50

let cache: VideoTaskRecord[] | null = null

function normalize(records: unknown): VideoTaskRecord[] {
  if (!Array.isArray(records)) return []
  const out: VideoTaskRecord[] = []
  for (const r of records) {
    if (!r || typeof r !== 'object') continue
    const rec = r as Record<string, unknown>
    if (typeof rec.taskId !== 'string' || rec.taskId === '') continue
    out.push({
      taskId: rec.taskId,
      model: typeof rec.model === 'string' ? rec.model : '',
      prompt: typeof rec.prompt === 'string' ? rec.prompt : '',
      duration:
        typeof rec.duration === 'number' && rec.duration > 0
          ? rec.duration
          : 0,
      size: typeof rec.size === 'string' ? rec.size : DEFAULT_SIZE,
      status: typeof rec.status === 'string' ? rec.status : 'queued',
      progress: typeof rec.progress === 'number' ? rec.progress : 0,
      createdAt:
        typeof rec.createdAt === 'number' && rec.createdAt > 0
          ? rec.createdAt
          : Math.floor(Date.now() / 1000),
      videoUrl: typeof rec.videoUrl === 'string' ? rec.videoUrl : undefined,
      errorMessage:
        typeof rec.errorMessage === 'string' ? rec.errorMessage : undefined,
      consumedInput:
        typeof rec.consumedInput === 'number' ? rec.consumedInput : undefined,
      consumedOutput:
        typeof rec.consumedOutput === 'number' ? rec.consumedOutput : undefined,
    })
  }
  return out
}

/** Load persisted history (newest first). Returns [] when empty/corrupt. */
export function loadVideoHistory(): VideoTaskRecord[] {
  if (cache) return cache
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    cache = normalize(raw ? JSON.parse(raw) : null)
  } catch {
    cache = []
  }
  return cache
}

/** Persist history (newest first) and keep the in-memory cache in sync. */
export function saveVideoHistory(records: VideoTaskRecord[]): void {
  cache = records
  try {
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify(records.slice(0, MAX_STORED_RECORDS))
    )
  } catch {
    // Private mode / quota exceeded — history simply won't survive reloads.
  }
}

/**
 * Merge server-side history (authoritative) with the local snapshot.
 *
 * The server never stores prompt/duration/size (they are submission-only
 * fields), so those are inherited from the local record when one exists.
 * Status/progress/videoUrl come from the server, which is fresher. Local-only
 * records (a submission not yet visible server-side, or tasks since purged)
 * are kept untouched. Returns a single newest-first list.
 */
export function mergeServerHistory(
  local: VideoTaskRecord[],
  server: VideoHistoryItem[]
): VideoTaskRecord[] {
  const localById = new Map(local.map((r) => [r.taskId, r]))
  const merged = new Map<string, VideoTaskRecord>()
  for (const item of server) {
    const ex = localById.get(item.task_id)
    merged.set(item.task_id, {
      taskId: item.task_id,
      model: item.model || ex?.model || '',
      prompt: ex?.prompt ?? '',
      duration: ex?.duration ?? (item.seconds ? Number(item.seconds) : 0),
      size: ex?.size ?? (item.size ?? item.resolution ?? DEFAULT_SIZE),
      status: item.status,
      progress: item.status === 'completed' ? 100 : item.progress,
      createdAt:
        item.created_at ||
        ex?.createdAt ||
        Math.floor(Date.now() / 1000),
      videoUrl: item.url || ex?.videoUrl,
      errorMessage:
        item.status === 'failed'
          ? (item.fail_reason || ex?.errorMessage)
          : ex?.errorMessage,
      consumedInput: ex?.consumedInput,
      consumedOutput: ex?.consumedOutput,
    })
  }
  for (const r of local) {
    if (!merged.has(r.taskId)) merged.set(r.taskId, r)
  }
  return [...merged.values()].sort((a, b) => b.createdAt - a.createdAt)
}
