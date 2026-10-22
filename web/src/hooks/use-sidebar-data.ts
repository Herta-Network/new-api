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
import {
  Activity,
  Box,
  ClipboardList,
  CreditCard,
  FileText,
  FlaskConical,
  Key,
  LayoutDashboard,
  ListTodo,
  MessageSquare,
  PlugZap,
  Radio,
  Receipt,
  ServerCog,
  Settings,
  ShieldCheck,
  Ticket,
  User,
  Users,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import type { NavItem, SidebarData } from '@/components/layout/types'
import {
  DASHBOARD_SECTION_TITLE_KEYS,
  DASHBOARD_SECTIONS,
} from '@/features/dashboard/section-registry'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import type { Workspace } from '@/lib/workspace'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups of a workspace's sidebar.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`. The user workspace
 * holds the signed-in user's own pages; the admin workspace holds the
 * site-wide views and the site administration pages.
 */
export function useSidebarData(workspace: Workspace): SidebarData {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)

  if (workspace === 'user') {
    return {
      navGroups: [
        {
          id: 'chat',
          title: t('Chat'),
          items: [
            {
              title: t('Playground'),
              url: '/user/playground',
              icon: FlaskConical,
            },
            {
              title: t('Chat'),
              icon: MessageSquare,
              type: 'chat-presets',
            },
          ],
        },
        {
          id: 'general',
          title: t('General'),
          items: [
            {
              title: t('Overview'),
              url: '/user/dashboard/overview',
              icon: Activity,
            },
            {
              title: t('Dashboard'),
              url: '/user/dashboard/models',
              activeUrls: ['/user/dashboard/flow'],
              icon: LayoutDashboard,
            },
            {
              title: t('API Keys'),
              url: '/user/keys',
              icon: Key,
            },
            {
              title: t('Usage Logs'),
              url: '/user/usage-logs/common',
              icon: FileText,
            },
            {
              title: t('Audit Logs'),
              url: '/user/usage-logs/audit',
              icon: ClipboardList,
            },
            {
              title: t('Task Logs'),
              url: '/user/usage-logs/task',
              activeUrls: ['/user/usage-logs/drawing'],
              configUrls: ['/user/usage-logs/drawing', '/user/usage-logs/task'],
              icon: ListTodo,
            },
          ],
        },
        {
          id: 'personal',
          title: t('Personal'),
          items: [
            {
              title: t('Wallet'),
              url: '/user/wallet',
              icon: Wallet,
            },
            {
              title: t('Profile'),
              url: '/user/profile',
              icon: User,
            },
            {
              title: t('Security & Access'),
              url: '/user/security',
              icon: ShieldCheck,
            },
          ],
        },
      ],
    }
  }

  const siteItems: NavItem[] = [
    {
      title: t('Overview'),
      url: '/admin',
      icon: Activity,
    },
    {
      title: t('Analytics'),
      icon: LayoutDashboard,
      items: DASHBOARD_SECTIONS.site.map((section) => ({
        title: t(DASHBOARD_SECTION_TITLE_KEYS[section]),
        url: `/admin/dashboard/${section}`,
      })),
    },
    {
      title: t('Usage Logs'),
      url: '/admin/usage-logs/common',
      icon: FileText,
    },
  ]
  // Without the audit grant the page could only show the admin's own
  // records, which the user workspace already lists.
  if (
    user &&
    hasPermission(
      user,
      ADMIN_PERMISSION_RESOURCES.AUDIT,
      ADMIN_PERMISSION_ACTIONS.READ
    )
  ) {
    siteItems.push({
      title: t('Audit Logs'),
      url: '/admin/usage-logs/audit',
      icon: ClipboardList,
    })
  }
  siteItems.push({
    title: t('Task Logs'),
    url: '/admin/usage-logs/task',
    activeUrls: ['/admin/usage-logs/drawing'],
    icon: ListTodo,
  })

  return {
    navGroups: [
      {
        id: 'site',
        title: t('General'),
        items: siteItems,
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
          {
            title: t('Channels'),
            url: '/admin/channels',
            icon: Radio,
          },
          {
            title: t('Models'),
            url: '/admin/models/metadata',
            icon: Box,
          },
          {
            title: t('Users'),
            url: '/admin/users',
            icon: Users,
          },
          {
            title: t('Redemption Codes'),
            url: '/admin/redemption-codes',
            icon: Ticket,
          },
          {
            title: t('Subscriptions'),
            url: '/admin/subscriptions',
            icon: CreditCard,
          },
          {
            title: t('Payment Orders'),
            url: '/admin/payment-orders/orders',
            activeUrls: ['/admin/payment-orders'],
            icon: Receipt,
          },
          {
            title: t('System Info'),
            url: '/admin/system-info',
            icon: ServerCog,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('Task Plugins'),
            url: '/admin/task-plugins',
            icon: PlugZap,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('System Settings'),
            url: '/admin/system-settings/site',
            activeUrls: ['/admin/system-settings'],
            icon: Settings,
            requiredRole: ROLE.SUPER_ADMIN,
          },
        ],
      },
    ],
  }
}
