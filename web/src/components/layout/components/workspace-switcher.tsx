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
import { useLocation, useNavigate } from '@tanstack/react-router'
import { ChevronsUpDown, ShieldCheck, UserRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import {
  getWorkspaceFromPathname,
  WORKSPACE_HOME,
  WORKSPACES,
  type Workspace,
} from '@/lib/workspace'

const WORKSPACE_OPTIONS: Record<
  Workspace,
  { labelKey: string; descriptionKey: string; icon: React.ElementType }
> = {
  user: {
    labelKey: 'User Console',
    descriptionKey: 'Your keys, usage and billing',
    icon: UserRound,
  },
  admin: {
    labelKey: 'Admin Console',
    descriptionKey: 'Site operations and settings',
    icon: ShieldCheck,
  },
}

/**
 * Switches between the user and admin workspaces from the top of the
 * sidebar. Only administrators get it; choosing a workspace opens its home.
 */
export function WorkspaceSwitcher() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { isMobile, setOpenMobile } = useSidebar()
  const pathname = useLocation({ select: (location) => location.pathname })
  const activeWorkspace = getWorkspaceFromPathname(pathname) ?? 'user'
  const active = WORKSPACE_OPTIONS[activeWorkspace]

  const handleWorkspaceChange = (value: unknown) => {
    const nextWorkspace = WORKSPACES.find((workspace) => workspace === value)
    if (!nextWorkspace || nextWorkspace === activeWorkspace) return
    setOpenMobile(false)
    void navigate({ to: WORKSPACE_HOME[nextWorkspace] })
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton size='lg' tooltip={t(active.labelKey)} />
            }
          >
            <span className='bg-primary text-primary-foreground flex aspect-square size-8 shrink-0 items-center justify-center rounded-lg'>
              <active.icon className='size-4' aria-hidden='true' />
            </span>
            <span className='grid min-w-0 flex-1 text-start leading-tight'>
              <span className='truncate font-semibold'>
                {t(active.labelKey)}
              </span>
              <span className='text-muted-foreground truncate text-xs'>
                {t(active.descriptionKey)}
              </span>
            </span>
            <ChevronsUpDown className='ms-auto size-4' aria-hidden='true' />
          </DropdownMenuTrigger>
          <DropdownMenuContent
            side={isMobile ? 'bottom' : 'right'}
            align='start'
            className='min-w-56'
          >
            <DropdownMenuGroup>
              <DropdownMenuLabel>{t('Switch console')}</DropdownMenuLabel>
              <DropdownMenuRadioGroup
                value={activeWorkspace}
                onValueChange={handleWorkspaceChange}
              >
                {WORKSPACES.map((workspace) => {
                  const option = WORKSPACE_OPTIONS[workspace]
                  return (
                    <DropdownMenuRadioItem
                      key={workspace}
                      value={workspace}
                      closeOnClick
                      className='gap-2 p-2'
                    >
                      <span className='flex size-6 items-center justify-center rounded-md border'>
                        <option.icon className='size-4' aria-hidden='true' />
                      </span>
                      {t(option.labelKey)}
                    </DropdownMenuRadioItem>
                  )
                })}
              </DropdownMenuRadioGroup>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
