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
import { describe, expect, test } from 'vitest'

import { resolveLegacyRoute } from './legacy-route'

describe('legacy frontend route migration', () => {
  test('maps former public and console routes to their current destinations', () => {
    const routes = {
      '/login': '/sign-in',
      '/forbidden': '/403',
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
      '/console/chat/42': '/user/chat/42',
    }

    for (const [source, target] of Object.entries(routes)) {
      expect(resolveLegacyRoute(source)).toBe(target)
    }
  })

  test('preserves search and hash while applying route-specific behavior', () => {
    expect(resolveLegacyRoute('/login?redirect=%2Fkeys#continue')).toBe(
      '/sign-in?redirect=%2Fkeys#continue'
    )
    expect(resolveLegacyRoute('/console/topup?source=email#orders')).toBe(
      '/user/wallet?source=email#orders'
    )
  })

  test('maps legacy settings tabs and retains unrelated parameters', () => {
    const settingsTabs = {
      operation: '/admin/system-settings/operations/behavior',
      dashboard: '/admin/system-settings/content/dashboard',
      chats: '/admin/system-settings/content/chat',
      drawing: '/admin/system-settings/content/drawing',
      payment: '/admin/system-settings/billing/payment',
      ratio: '/admin/models/metadata',
      ratelimit: '/admin/system-settings/security/rate-limit',
      models: '/admin/system-settings/models/global',
      'model-deployment': '/admin/system-settings/models/model-deployment',
      performance: '/admin/system-settings/operations/performance',
      system: '/admin/system-settings/site/system-info',
      other: '/admin/system-settings/site/system-info',
    }

    for (const [tab, target] of Object.entries(settingsTabs)) {
      expect(
        resolveLegacyRoute(`/console/setting?tab=${tab}&from=bookmark#form`)
      ).toBe(`${target}?tab=${tab}&from=bookmark#form`)
    }
    expect(resolveLegacyRoute('/console/setting?tab=unknown')).toBe(
      '/admin/system-settings?tab=unknown'
    )
  })

  test('safely redirects unknown console locations without touching new routes', () => {
    expect(resolveLegacyRoute('/console/removed?page=2#old')).toBe(
      '/user/dashboard?page=2#old'
    )
    expect(resolveLegacyRoute('/user/dashboard')).toBe(null)
    expect(resolveLegacyRoute('/admin/users')).toBe(null)
    expect(resolveLegacyRoute('/api/status')).toBe(null)
  })

  test('moves root console pages into the user and admin workspaces', () => {
    const routes = {
      '/dashboard': '/user/dashboard',
      '/dashboard/overview': '/user/dashboard/overview',
      '/dashboard/users': '/admin/dashboard/users',
      '/playground': '/user/playground',
      '/chat/3': '/user/chat/3',
      '/chat2link': '/user/chat2link',
      '/keys': '/user/keys',
      '/usage-logs/common': '/user/usage-logs/common',
      '/usage-logs/audit': '/user/usage-logs/audit',
      '/wallet': '/user/wallet',
      '/profile': '/user/profile',
      '/security': '/user/security',
      '/channels/': '/admin/channels',
      '/models/deployments': '/admin/models/deployments',
      '/users': '/admin/users',
      '/redemption-codes': '/admin/redemption-codes',
      '/subscriptions': '/admin/subscriptions',
      '/payment-orders/unmatched': '/admin/payment-orders/unmatched',
      '/system-info': '/admin/system-info',
      '/task-plugins': '/admin/task-plugins',
      '/system-settings/billing/payment-methods':
        '/admin/system-settings/billing/payment-methods',
    }

    for (const [source, target] of Object.entries(routes)) {
      expect(resolveLegacyRoute(source)).toBe(target)
    }
  })

  test('keeps the payment return search when moving the wallet', () => {
    expect(
      resolveLegacyRoute('/wallet?payment_order=P7&show_history=true')
    ).toBe('/user/wallet?payment_order=P7&show_history=true')
  })

  test('matches moved prefixes on whole path segments only', () => {
    expect(resolveLegacyRoute('/user/reset?email=a%40b.c&token=t')).toBe(null)
    expect(resolveLegacyRoute('/keyset')).toBe(null)
    expect(resolveLegacyRoute('/pricing')).toBe(null)
  })
})
