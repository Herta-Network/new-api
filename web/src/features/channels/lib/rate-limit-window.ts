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

/** One usage allowance window, as the Codex and OpenCode Go usage APIs report it. */
export type RateLimitWindowData = {
  used_percent?: number
  reset_at?: number
  reset_after_seconds?: number
  limit_window_seconds?: number
}

export function formatDurationSeconds(
  seconds: unknown,
  t: (key: string) => string
): string {
  const s = Number(seconds)
  if (!Number.isFinite(s) || s <= 0) {
    return '-'
  }

  const total = Math.floor(s)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const secs = total % 60

  if (hours > 0) {
    return `${hours}${t('h')} ${minutes}${t('m')}`
  }
  if (minutes > 0) {
    return `${minutes}${t('m')} ${secs}${t('s')}`
  }
  return `${secs}${t('s')}`
}
