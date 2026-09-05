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

// API endpoints for the video generation page.
// Submissions go through the playground session endpoints (/pg/videos)
// which reuse the dashboard session instead of an API token.
export const API_ENDPOINTS = {
  VIDEO_SUBMIT: '/pg/videos',
  VIDEO_TASK: (taskId: string) => `/pg/videos/${encodeURIComponent(taskId)}`,
  VIDEO_MODELS: '/api/user/video_models',
  VIDEO_TASKS: '/api/user/video_tasks',
  USER_GROUPS: '/api/user/self/groups',
} as const

// Polling configuration for async video tasks.
export const POLL_INTERVAL_MS = 5000
export const POLL_TIMEOUT_MS = 20 * 60 * 1000

// ---- model families ----
// Each video model family has its own upstream parameter contract; the form
// adapts its duration / resolution / aspect-ratio options to the selected
// model's family.

export type VideoModelFamily = 'minimax' | 'seedance' | 'wan' | 'generic'

export function videoModelFamily(model: string): VideoModelFamily {
  const m = (model || '').toLowerCase()
  if (m.startsWith('wan3.0') || m.startsWith('wan2') || m.startsWith('wanx')) {
    return 'wan'
  }
  if (m.includes('seedance') || m.startsWith('doubao')) return 'seedance'
  if (m.includes('minimax') || m.includes('h3')) return 'minimax'
  return 'generic'
}

// MiniMax-H3 (maitoken flat endpoint): seconds 5~15 (4s is rejected with 422);
// OpenAI-style size in pixels, long edge >= 1792 bills as 2K.
export const MINIMAX_PRESET = {
  durations: [5, 6, 8, 10, 12, 15],
  sizes: [
    { value: '720x1280', label: '720x1280 · 768P ↑' },
    { value: '1280x720', label: '1280x720 · 768P ↔' },
    { value: '1440x2560', label: '1440x2560 · 2K ↑' },
    { value: '2560x1440', label: '2560x1440 · 2K ↔' },
  ],
  defaultDuration: 5,
  defaultSize: '720x1280',
} as const

// Seedance (doubao native): duration 4~15s, resolution 480p/720p/1080p,
// ratio enum per the Volcano Ark contract. Submitted as
// metadata {resolution, ratio}; the doubao adaptor maps them natively.
export const SEEDANCE_PRESET = {
  durations: [4, 5, 6, 8, 10, 12, 15],
  resolutions: ['480p', '720p', '1080p'],
  ratios: ['16:9', '9:16', '4:3', '3:4', '1:1', '21:9'],
  defaultDuration: 5,
  defaultResolution: '720p',
  defaultRatio: '16:9',
} as const

// Flat-H3 variants (lowercase minimax-h3-*) are capped at the 720P tier —
// the upstream flat endpoint rejects 2K resolutions for these models. Only
// the capitalised "MiniMax-H3" (legacy endpoint) keeps the 2K tier.
// Per maitoken confirmation (2026-09-04): base / mini render at 720P only;
// base-fast targets the 768 tier of the new contract (upstream pending).
const FLAT_H3_NO_2K = new Set([
  'minimax-h3-base',
  'minimax-h3-base-fast',
  'minimax-h3-mini',
])

const FLAT_H3_TIER: Record<string, string> = {
  'minimax-h3-base-fast': '768P',
}

export function minimaxSizeOptions(
  model: string
): { value: string; label: string }[] {
  const m = (model || '').toLowerCase()
  if (!FLAT_H3_NO_2K.has(m)) {
    return [...MINIMAX_PRESET.sizes]
  }
  const tier = FLAT_H3_TIER[m] ?? '720P'
  return [
    { value: '720x1280', label: `720x1280 · ${tier} ↑` },
    { value: '1280x720', label: `1280x720 · ${tier} ↔` },
  ]
}

// Wan family (ali / DashScope video-synthesis): duration 2~30s plus the
// smart-duration sentinel (-1, billed as 30s upfront then settled on actual
// output length); resolution 480P/720P/1080P tiers; ratio goes through
// metadata.parameters.ratio (the gateway merges it into the upstream
// parameters object — size only carries the resolution tier).
export const WAN_SMART_DURATION = -1

export const WAN_PRESET = {
  durations: [2, 3, 4, 5, 6, 8, 10, 15, 20, 25, 30],
  resolutions: ['480P', '720P', '1080P'],
  ratios: ['adaptive', '16:9', '9:16', '4:3', '3:4', '1:1', '21:9'],
  defaultDuration: 5,
  defaultResolution: '720P',
  defaultRatio: 'adaptive',
  // Reference-video upload cap differs per family (wan3.0 upstream allows 5).
  maxReferenceVideos: 5,
} as const

// Rough CNY-per-second estimates for the wan family, derived from the
// configured model ratios (480P = base tier, 720P = 2x, 1080P = 4x; prime is
// 1.5x standard). Estimates only — actual billing settles on upstream usage.
export const WAN_PRICE_PER_SECOND: Record<string, Record<string, number>> = {
  'wan3.0-video': { '480P': 0.075, '720P': 0.15, '1080P': 0.299 },
  'wan3.0-video-prime': { '480P': 0.112, '720P': 0.225, '1080P': 0.449 },
}

// Legacy static options (kept for reference / other families fall back to
// the minimax-shaped form).
export const DURATION_OPTIONS = MINIMAX_PRESET.durations
export const SIZE_OPTIONS = MINIMAX_PRESET.sizes

export const DEFAULT_SIZE = MINIMAX_PRESET.defaultSize
export const DEFAULT_DURATION = MINIMAX_PRESET.defaultDuration

// Rough CNY-per-second estimates for the seedance family (per resolution),
// derived from the published doubao-seedance-2.0 prices (2.535 / 5.44 /
// 13.56 CNY per 5s at 480p / 720p / 1080p). Estimates only — actual billing
// is token-based on the upstream usage report.
export const SEEDANCE_PRICE_PER_SECOND: Record<string, number> = {
  '480p': 0.51,
  '720p': 1.09,
  '1080p': 2.71,
}

// Terminal task statuses.
export const TERMINAL_STATUSES = new Set(['completed', 'failed'])
