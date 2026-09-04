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
import { api } from '@/lib/api'

import { API_ENDPOINTS } from './constants'
import type {
  GroupOption,
  VideoHistoryItem,
  VideoSubmitRequest,
  VideoTaskResponse,
} from './types'

/** Extract a human-readable message from an error response. */
function extractErrorMessage(error: unknown, fallback: string): string {
  const err = error as {
    response?: { data?: { error?: { message?: string }; message?: string } }
    message?: string
  }
  return (
    err?.response?.data?.error?.message ||
    err?.response?.data?.message ||
    err?.message ||
    fallback
  )
}

/**
 * Submit a video generation task via the playground session endpoint.
 * Returns the created task (public task id + initial status).
 */
export async function submitVideoTask(
  payload: VideoSubmitRequest
): Promise<VideoTaskResponse> {
  try {
    const res = await api.post(API_ENDPOINTS.VIDEO_SUBMIT, payload, {
      skipErrorHandler: true,
    } as Record<string, unknown>)
    return res.data as VideoTaskResponse
  } catch (error) {
    throw new Error(extractErrorMessage(error, 'Failed to submit video task'))
  }
}

/**
 * Query a video task by its public task id.
 * Returns null when the task does not exist (e.g. purged).
 */
export async function getVideoTask(
  taskId: string
): Promise<VideoTaskResponse | null> {
  try {
    const res = await api.get(API_ENDPOINTS.VIDEO_TASK(taskId), {
      skipErrorHandler: true,
    } as Record<string, unknown>)
    return res.data as VideoTaskResponse
  } catch (error) {
    const status = (error as { response?: { status?: number } })?.response
      ?.status
    if (status && status >= 400 && status < 500) {
      // Task not found / not accessible — stop polling with null.
      return null
    }
    // Transient errors (network / 5xx): keep polling.
    throw error
  }
}

/**
 * Fetch the current user's video-generation history from the server
 * (GET /api/user/video_tasks). This is the durable source of truth: it
 * survives reloads, browser changes and localStorage clearing.
 * Returns [] on failure so callers can fall back to localStorage.
 */
export async function fetchVideoTaskHistory(): Promise<VideoHistoryItem[]> {
  const res = await api.get(API_ENDPOINTS.VIDEO_TASKS)
  const { data } = res
  if (!data?.success || !Array.isArray(data.data)) {
    return []
  }
  return data.data as VideoHistoryItem[]
}

/**
 * Upload a local file to the gateway asset storage (OSS when configured).
 * Returns the publicly reachable URL of the stored file.
 */
export async function uploadAssetFile(file: File): Promise<string> {
  const form = new FormData()
  form.append('file', file)
  try {
    const res = await api.post('/api/asset/upload', form, {
      skipErrorHandler: true,
    } as Record<string, unknown>)
    const body = res.data as {
      success?: boolean
      message?: string
      data?: { Url?: string }
    }
    if (!body?.success || !body.data?.Url) {
      throw new Error(body?.message || 'Upload failed')
    }
    return body.data.Url
  } catch (error) {
    const err = error as {
      response?: { data?: { message?: string } }
      message?: string
    }
    throw new Error(
      err?.response?.data?.message || err?.message || 'Upload failed'
    )
  }
}

/**
 * Get video-capable models available to the current user
 * (models whose channels are dedicated video channels or have task pricing).
 */
export async function getVideoModels(): Promise<string[]> {
  const res = await api.get(API_ENDPOINTS.VIDEO_MODELS)
  const { data } = res
  if (!data?.success || !Array.isArray(data.data)) {
    return []
  }
  return data.data as string[]
}

/**
 * Get user groups (same endpoint as the playground group selector).
 */
export async function getUserGroups(): Promise<GroupOption[]> {
  const res = await api.get(API_ENDPOINTS.USER_GROUPS)
  const { data } = res
  if (!data?.success || !data.data) {
    return []
  }
  const groupData = data.data as Record<
    string,
    { desc: string; ratio: number }
  >
  return Object.entries(groupData).map(([group, info]) => ({
    label: group,
    value: group,
    ratio: info.ratio,
    desc: info.desc,
  }))
}
