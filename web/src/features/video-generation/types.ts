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

/** Group option for the group selector (same shape as playground). */
export interface GroupOption {
  label: string
  value: string
  ratio: number
  desc: string
}

/** Body for POST /pg/videos (OpenAI-compatible video submission). */
export interface VideoSubmitRequest {
  model: string
  prompt: string
  duration?: number
  size?: string
  image?: string
  reference_video_urls?: string[]
  group?: string
  /** Native upstream fields (e.g. seedance: {resolution, ratio}). */
  metadata?: Record<string, unknown>
}

/**
 * Task object returned by POST /pg/videos and GET /pg/videos/:task_id
 * (OpenAI Video API format, relayed through the sora task channel).
 */
export interface VideoTaskResponse {
  id: string
  task_id?: string
  object?: string
  model?: string
  status: string // queued | unknown | processing | in_progress | completed | failed
  progress?: number
  created_at?: number
  completed_at?: number
  expires_at?: number
  seconds?: string
  size?: string
  error?: { message?: string; code?: string }
  metadata?: { url?: string }
  // Billing split injected by the gateway on query (MiniMax-H3 series only).
  consumed_input_quota?: number
  consumed_input_amount?: number
  consumed_output_quota?: number
  consumed_output_amount?: number
}

/** A task tracked in the page's local session history. */
export interface VideoTaskRecord {
  taskId: string
  model: string
  prompt: string
  duration: number
  size: string
  status: string
  progress: number
  createdAt: number
  videoUrl?: string
  errorMessage?: string
  consumedInput?: number
  consumedOutput?: number
}

/**
 * One entry of GET /api/user/video_tasks — the server-side task history
 * (denormalized from the tasks table; durable across browsers/devices).
 */
export interface VideoHistoryItem {
  task_id: string
  status: string
  progress: number
  model: string
  seconds?: string
  resolution?: string
  size?: string
  mode?: string
  aspect_ratio?: string
  url?: string
  filename?: string
  created_at: number
  updated_at: number
  fail_reason?: string
}
