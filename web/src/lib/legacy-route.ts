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
const legacyOrigin = 'https://legacy-route.invalid'

const legacyConsoleRoutes: Record<string, string> = {
  '/console': '/user/dashboard',
  '/console/models': '/admin/models',
  '/console/deployment': '/admin/models/deployments',
  '/console/subscription': '/admin/subscriptions',
  '/console/channel': '/admin/channels',
  '/console/token': '/user/keys',
  '/console/playground': '/user/playground',
  '/console/redemption': '/admin/redemption-codes',
  '/console/user': '/admin/users',
  '/console/personal': '/user/profile',
  '/console/log': '/user/usage-logs',
  '/console/midjourney': '/user/usage-logs/drawing',
  '/console/task': '/user/usage-logs/task',
}

const legacySettingsTabs: Record<string, string> = {
  operation: '/admin/system-settings/operations/behavior',
  dashboard: '/admin/system-settings/content/dashboard',
  chats: '/admin/system-settings/content/chat',
  drawing: '/admin/system-settings/content/drawing',
  payment: '/admin/system-settings/billing/payment',
  ratio: '/admin/system-settings/billing/model-pricing',
  ratelimit: '/admin/system-settings/security/rate-limit',
  models: '/admin/system-settings/models/global',
  'model-deployment': '/admin/system-settings/models/model-deployment',
  performance: '/admin/system-settings/operations/performance',
  system: '/admin/system-settings/site/system-info',
  other: '/admin/system-settings/site/system-info',
}

// Console pages that sat at the root before the console split into the /user
// and /admin workspaces. A path under a prefix keeps the rest of its path, so
// bookmarks and links in emails already sent still open the same page. The
// first matching prefix wins.
const movedConsolePrefixes: readonly (readonly [string, string])[] = [
  ['/dashboard/users', '/admin/dashboard/users'],
  ['/dashboard', '/user/dashboard'],
  ['/playground', '/user/playground'],
  ['/chat2link', '/user/chat2link'],
  ['/chat', '/user/chat'],
  ['/keys', '/user/keys'],
  ['/usage-logs', '/user/usage-logs'],
  ['/wallet', '/user/wallet'],
  ['/profile', '/user/profile'],
  ['/security', '/user/security'],
  ['/channels', '/admin/channels'],
  ['/models', '/admin/models'],
  ['/users', '/admin/users'],
  ['/redemption-codes', '/admin/redemption-codes'],
  ['/subscriptions', '/admin/subscriptions'],
  ['/payment-orders', '/admin/payment-orders'],
  ['/system-info', '/admin/system-info'],
  ['/task-plugins', '/admin/task-plugins'],
  ['/system-settings', '/admin/system-settings'],
]

function normalizeLegacyPath(pathname: string): string {
  if (pathname === '/') return pathname
  return pathname.replace(/\/+$/, '')
}

function buildTargetHref(targetPath: string, source: URL): string {
  const target = new URL(targetPath, legacyOrigin)
  source.searchParams.forEach((value, key) => {
    target.searchParams.append(key, value)
  })
  target.hash = source.hash
  return `${target.pathname}${target.search}${target.hash}`
}

export function resolveLegacyRoute(rawHref: string): string | null {
  let source: URL
  try {
    source = new URL(rawHref, legacyOrigin)
  } catch {
    return null
  }

  const pathname = normalizeLegacyPath(source.pathname)
  if (pathname === '/login') {
    return buildTargetHref('/sign-in', source)
  }
  if (pathname === '/forbidden') {
    return buildTargetHref('/403', source)
  }
  if (pathname === '/console/topup') {
    return buildTargetHref('/user/wallet', source)
  }
  if (pathname === '/console/setting') {
    const tab = source.searchParams.get('tab') ?? ''
    const target = legacySettingsTabs[tab] ?? '/admin/system-settings'
    return buildTargetHref(target, source)
  }
  if (pathname === '/console/chat') {
    return buildTargetHref('/user/dashboard', source)
  }
  if (pathname.startsWith('/console/chat/')) {
    const chatID = pathname.slice('/console/chat/'.length)
    return buildTargetHref(
      chatID ? `/user/chat/${chatID}` : '/user/dashboard',
      source
    )
  }

  const target = legacyConsoleRoutes[pathname]
  if (target) return buildTargetHref(target, source)
  if (pathname.startsWith('/console/')) {
    return buildTargetHref('/user/dashboard', source)
  }

  for (const [prefix, destination] of movedConsolePrefixes) {
    if (pathname === prefix || pathname.startsWith(`${prefix}/`)) {
      return buildTargetHref(
        destination + pathname.slice(prefix.length),
        source
      )
    }
  }

  return null
}
