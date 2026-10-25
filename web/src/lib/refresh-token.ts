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
 * The refresh token of the browser's login session.
 *
 * The console keeps it in localStorage so a session survives reloads and is
 * shared by every tab, also when the console and the backend are on different
 * sites, where a cookie of the backend would be a blocked third-party cookie.
 * Script on this origin can read it, so the session's other limits matter:
 * every refresh replaces it, a replaced token used again ends the session, and
 * the session ends after 30 days or at sign-out.
 *
 * The token has the form `<sid>.<secret>`; the SID tells which session a
 * stored token belongs to without a request.
 */
export const REFRESH_TOKEN_STORAGE_KEY = 'new_api_refresh_token'

/**
 * The script-readable marker the backend wrote beside its legacy refresh
 * cookie. A session signed in before the console kept the token itself still
 * has that cookie, which the backend accepts for one refresh.
 */
export const LEGACY_SESSION_HINT_COOKIE_NAME = 'new_api_has_session'

/** Read a cookie value out of a `document.cookie`-shaped string. */
export function readCookie(cookieHeader: string, name: string): string | null {
  for (const part of cookieHeader.split(';')) {
    const separator = part.indexOf('=')
    if (separator < 0) continue
    if (part.slice(0, separator).trim() !== name) continue
    return part.slice(separator + 1).trim()
  }
  return null
}

/** The stored refresh token, or `null` when there is none or storage fails. */
export function readRefreshToken(): string | null {
  try {
    return window.localStorage.getItem(REFRESH_TOKEN_STORAGE_KEY) || null
  } catch {
    return null
  }
}

/** Keep the refresh token a login or refresh returned. */
export function storeRefreshToken(token: string): void {
  if (!token) return
  try {
    window.localStorage.setItem(REFRESH_TOKEN_STORAGE_KEY, token)
  } catch {
    // Without storage the session lasts until the access token expires.
  }
}

/** The SID of a refresh token, or `''` when it has none. */
export function refreshTokenSID(token: string): string {
  const separator = token.indexOf('.')
  return separator > 0 ? token.slice(0, separator) : ''
}

/**
 * Remove the stored refresh token, but only while it is still the one that
 * ended: the token `token`, or any token of session `sid`. Another tab may
 * already have stored the token of a newer sign-in, which must survive.
 */
export function forgetRefreshToken(match: { sid?: string; token?: string }) {
  const stored = readRefreshToken()
  if (!stored) return
  const matches =
    (match.token !== undefined && stored === match.token) ||
    (match.sid !== undefined && refreshTokenSID(stored) === match.sid)
  if (!matches) return
  try {
    window.localStorage.removeItem(REFRESH_TOKEN_STORAGE_KEY)
  } catch {
    // Nothing more to do when storage is unavailable.
  }
}

/**
 * Delete the legacy session marker once a refresh without a stored token is
 * refused. The backend deletes it itself when it shares the console's host;
 * on a console served from another host only the page can.
 */
export function forgetLegacySessionHint(): void {
  if (typeof document === 'undefined') return
  document.cookie = `${LEGACY_SESSION_HINT_COOKIE_NAME}=; path=/; max-age=0`
}

/**
 * Whether a refresh may succeed: the browser keeps a refresh token, or still
 * has the legacy session marker of a session signed in before the change.
 *
 * Returns `true` when neither can be read at all (no `window`, as in SSR or a
 * non-DOM test environment): an unreadable hint is not evidence of absence.
 */
export function hasSessionHint(): boolean {
  if (typeof window === 'undefined' || typeof document === 'undefined') {
    return true
  }
  if (readRefreshToken()) return true
  return readCookie(document.cookie, LEGACY_SESSION_HINT_COOKIE_NAME) !== null
}
