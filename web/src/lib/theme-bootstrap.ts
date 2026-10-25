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
 * The `<head>` script that applies the saved theme class to `<html>` before
 * the first paint, so the page never flashes light and one-shot detectors
 * (e.g. Dark Reader) see the real theme. It reads the key `ThemeProvider`
 * stores the mode under (`THEME_STORAGE_KEYS.mode`).
 *
 * The build emits it as a file next to the bundle rather than inline, so the
 * console's Content-Security-Policy allows scripts from its own origin only.
 * The script is ES5 so it runs before, and regardless of, the bundle's
 * browser target.
 */
export function themeBootstrapScript(modeStorageKey: string): string {
  return `;(function () {
  var theme = 'system'
  try {
    theme = localStorage.getItem(${JSON.stringify(modeStorageKey)}) || 'system'
  } catch (e) {}
  if (theme !== 'dark' && theme !== 'light') {
    theme = window.matchMedia('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'light'
  }
  document.documentElement.classList.add(theme)
})()`
}
