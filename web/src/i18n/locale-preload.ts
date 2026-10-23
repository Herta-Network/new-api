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
 * The inline `<head>` script that starts downloading the visitor's locale
 * chunk alongside the main bundle.
 *
 * Without it the chunk is requested only after the main bundle has run
 * `initI18n`, and the first paint waits for that extra round trip. The script
 * picks the language the way `initI18n` does: the saved `i18nextLng`, then the
 * browser languages, each mapped by `convertDetectedLanguage`; the first code
 * that is a supported language wins, then the first whose language part
 * (before `-` or `_`) is one (i18next's `supportedLngs` matching), and English
 * otherwise. It adds a `<link rel="preload" as="script">` for that one chunk;
 * the chunk loader's `<script>` for the same URL then takes the preloaded
 * response instead of downloading it again. `__tests__/locale-preload.test.ts`
 * checks the choice against `initI18n` for every language.
 *
 * The preload has the low priority of the bundle's deferred scripts, so it
 * never holds them back: the bundle still arrives first and runs while the
 * locale finishes downloading.
 *
 * `chunkUrls` maps every interface language code to its chunk URL. The script
 * is ES5 so it runs before, and regardless of, the bundle's browser target.
 */
export function localePreloadScript(chunkUrls: Record<string, string>): string {
  return `;(function () {
  var urls = ${JSON.stringify(chunkUrls)}
  var codes = []
  try {
    var saved = localStorage.getItem('i18nextLng')
    if (saved) codes.push(saved)
  } catch (e) {}
  var nav = navigator
  for (var i = 0; nav.languages && i < nav.languages.length; i++) {
    codes.push(nav.languages[i])
  }
  if (nav.userLanguage) codes.push(nav.userLanguage)
  if (nav.language) codes.push(nav.language)
  var lang
  for (i = 0; i < codes.length; i++) {
    var lower = codes[i].trim().replace(/_/g, '-').toLowerCase()
    if (lower === 'zhtw' || lower === 'zh-tw' || lower === 'zh-hk' ||
        lower === 'zh-mo' || lower.indexOf('zh-hant') === 0) {
      codes[i] = 'zhTW'
    } else if (lower.indexOf('zh') === 0) {
      codes[i] = 'zhCN'
    }
    if (!lang && urls.hasOwnProperty(codes[i])) lang = codes[i]
  }
  for (i = 0; !lang && i < codes.length; i++) {
    var base = codes[i].replace(/_/g, '-').split('-')[0]
    if (urls.hasOwnProperty(base)) lang = base
  }
  var link = document.createElement('link')
  link.rel = 'preload'
  link.as = 'script'
  link.setAttribute('fetchpriority', 'low')
  link.href = urls[lang || 'en']
  document.head.appendChild(link)
})()`
}
