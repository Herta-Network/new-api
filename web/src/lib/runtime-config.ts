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

/**
 * Runtime configuration of a deployed console, read from
 * `/runtime-config.json` next to `index.html` before the app starts, so the
 * backend address can change without rebuilding the frontend.
 *
 * `apiBaseUrl` is the public address of the backend when the console is
 * served from another origin, such as `https://api.example.com`. Empty means
 * the backend serves the console itself and requests stay same-origin.
 */
export const RUNTIME_CONFIG_PATH = '/runtime-config.json'

let apiBaseUrl = ''

/**
 * Validate a configured backend address: an http(s) URL without credentials,
 * query or fragment. A path is kept for a backend behind a path prefix.
 * Anything else yields `''`, the same-origin default.
 */
export function parseApiBaseUrl(value: unknown): string {
  if (typeof value !== 'string' || !value.trim()) return ''
  try {
    const url = new URL(value.trim())
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return ''
    if (url.username || url.password || url.search || url.hash) return ''
    return `${url.origin}${url.pathname}`.replace(/\/+$/, '')
  } catch {
    return ''
  }
}

/**
 * Read the runtime configuration. A missing, unreadable or invalid file keeps
 * the same-origin default, so a console the backend serves needs none.
 */
export async function loadRuntimeConfig(
  fetcher: typeof fetch = fetch
): Promise<void> {
  try {
    const response = await fetcher(RUNTIME_CONFIG_PATH, { cache: 'no-store' })
    if (!response.ok) return
    const config: unknown = await response.json()
    if (config && typeof config === 'object' && 'apiBaseUrl' in config) {
      apiBaseUrl = parseApiBaseUrl(config.apiBaseUrl)
    }
  } catch {
    // A static host may answer with index.html; keep the default.
  }
}

/** The configured backend address, or `''` for same-origin requests. */
export function getApiBaseUrl(): string {
  return apiBaseUrl
}

/** Override the backend address; for tests. */
export function setApiBaseUrl(value: string): void {
  apiBaseUrl = parseApiBaseUrl(value)
}

/**
 * The URL of a backend path such as `/api/status`, for requests and links
 * that do not go through the shared axios instances.
 */
export function apiUrl(path: string): string {
  return apiBaseUrl ? `${apiBaseUrl}${path}` : path
}

/**
 * The public backend address shown to users as the API base URL when the
 * server does not report one: the configured backend, else this page's
 * origin.
 */
export function apiOrigin(): string {
  if (apiBaseUrl) return apiBaseUrl
  return typeof window === 'undefined' ? '' : window.location.origin
}
