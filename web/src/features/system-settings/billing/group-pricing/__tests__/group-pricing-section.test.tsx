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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { GroupPricingSection, type GroupPricingValues } from '..'
import { SettingsPageProvider } from '../../../components/settings-page-context'

const saved: GroupPricingValues = {
  GroupRatio: '{"default":1,"vip":0.8,"premium":0.5,"internal":2}',
  UserUsableGroups: '{"default":"Standard","premium":"Premium","auto":"Auto"}',
  GroupGroupRatio: '{"vip":{"premium":0.3}}',
  AutoGroups: '["premium","default"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{"vip":{"-:default":"remove"}}',
  TopupGroupRatio: '{"vip":1.2}',
}

const pricing = {
  success: true,
  data: [
    {
      model_name: 'chat-model',
      quota_type: 0,
      model_ratio: 1.5,
      completion_ratio: 4,
      enable_groups: ['default', 'premium'],
    },
  ],
  vendors: [],
  group_ratio: {},
  usable_group: {},
  supported_endpoint: {},
  auto_groups: [],
}

function Page(props: { values: GroupPricingValues }) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [status, setStatus] = useState<HTMLSpanElement | null>(null)
  return (
    <SettingsPageProvider
      actionsContainer={actions}
      titleStatusContainer={status}
    >
      <span ref={setStatus} />
      <div ref={setActions} />
      <GroupPricingSection defaultValues={props.values} paymentCurrency='USD' />
    </SettingsPageProvider>
  )
}

function renderPage(values: Partial<GroupPricingValues> = {}) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/group/usage') {
      return {
        data: {
          success: true,
          data: { users: { default: 3, vip: 1 }, tokens: { premium: 2 } },
        },
      }
    }
    if (url === '/api/pricing') return { data: pricing }
    return { data: { success: true, data: {} } }
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true, message: '' } })
  const root = createRootRoute()
  const page = createRoute({
    getParentRoute: () => root,
    path: '/admin/system-settings/billing/$section',
    component: () => <Page values={{ ...saved, ...values }} />,
  })
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({
      initialEntries: ['/admin/system-settings/billing/group-pricing'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { put }
}

function rowOf(name: string) {
  const table = screen.getByRole('table', { name: 'Groups' })
  const cell = within(table).getByText(name, { selector: 'span' })
  const row = cell.closest('tr')
  if (!row) throw new Error(`no row for ${name}`)
  return row
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('group pricing page', () => {
  it('lists every group with its ratio, who can use it and its usage', async () => {
    renderPage()
    await screen.findByRole('table', { name: 'Groups' })
    await waitFor(() => expect(rowOf('default')).toHaveTextContent('3'))
    expect(rowOf('default')).toHaveTextContent('×1')
    expect(rowOf('default')).toHaveTextContent('User selectable')
    expect(rowOf('default')).toHaveTextContent('Hidden from vip')
    expect(rowOf('vip')).toHaveTextContent('Admin assigned')
    expect(rowOf('premium')).toHaveTextContent('2')
    expect(rowOf('internal')).toHaveTextContent('Hidden')
    expect(rowOf('auto')).toHaveTextContent('Group it lands in')
  })

  it('saves added and edited groups without sending the options it did not touch', async () => {
    const user = userEvent.setup()
    const { put } = renderPage()
    await screen.findByRole('table', { name: 'Groups' })

    await user.click(screen.getByRole('button', { name: 'Add group' }))
    const addDrawer = await screen.findByRole('dialog')
    await user.type(within(addDrawer).getByLabelText('Group name'), 'team')
    const ratio = within(addDrawer).getByLabelText('Price ratio')
    await user.clear(ratio)
    await user.type(ratio, '0.65')
    await user.type(within(addDrawer).getByLabelText('Description'), 'Team')
    await user.click(
      within(addDrawer).getByRole('button', { name: 'Add group' })
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(screen.getByRole('button', { name: 'Edit group vip' }))
    const editDrawer = await screen.findByRole('dialog')
    expect(within(editDrawer).getByLabelText('Group name')).toBeDisabled()
    expect(within(editDrawer).getByText('Top-up ratio')).toBeVisible()
    const vipRatio = within(editDrawer).getByLabelText('Price ratio')
    await user.clear(vipRatio)
    await user.type(vipRatio, '0.75')
    await user.click(within(editDrawer).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    expect(screen.getByText('Unsaved changes')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )

    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(put.mock.calls.map((call) => call[1])).toEqual([
      {
        key: 'GroupRatio',
        value:
          '{"default":1,"vip":0.75,"premium":0.5,"internal":2,"team":0.65}',
      },
      {
        key: 'UserUsableGroups',
        value:
          '{"default":"Standard","premium":"Premium","auto":"Auto","team":"Team"}',
      },
    ])
    await waitFor(() =>
      expect(screen.queryByText('Unsaved changes')).toBeNull()
    )
  })

  it('keeps edits across the JSON and visual editors and never edits invalid JSON visually', async () => {
    const user = userEvent.setup()
    const { put } = renderPage()
    await screen.findByRole('table', { name: 'Groups' })

    await user.click(
      screen.getByRole('button', { name: /Inter-group overrides/ })
    )
    const overrides = screen.getByRole('table', {
      name: 'Inter-group overrides',
    })
    expect(within(overrides).getByText('×0.3')).toBeVisible()

    const mode = screen.getByRole('group', {
      name: 'Editing mode for Inter-group overrides',
    })
    await user.click(within(mode).getByRole('button', { name: 'JSON' }))
    const editor = screen.getByRole('textbox', {
      name: 'Inter-group overrides',
    })
    fireEvent.input(editor, {
      target: { value: '{"vip":{"premium":0.25}}' },
    })
    await user.click(within(mode).getByRole('button', { name: 'Visual' }))
    expect(
      within(
        screen.getByRole('table', { name: 'Inter-group overrides' })
      ).getByText('×0.25')
    ).toBeVisible()

    await user.click(within(mode).getByRole('button', { name: 'JSON' }))
    fireEvent.input(
      screen.getByRole('textbox', { name: 'Inter-group overrides' }),
      { target: { value: '{"vip":{"premium":0.25}' } }
    )
    await user.click(within(mode).getByRole('button', { name: 'Visual' }))
    expect(
      screen.getByText('Inter-group overrides is not valid JSON')
    ).toBeVisible()
    expect(
      screen.queryByRole('table', { name: 'Inter-group overrides' })
    ).toBeNull()

    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await user.click(screen.getByRole('button', { name: 'Edit as JSON' }))
    expect(
      screen.getByRole('textbox', { name: 'Inter-group overrides' })
    ).toHaveValue('{"vip":{"premium":0.25}')
    expect(put).not.toHaveBeenCalled()
  })

  it('simulates a call step by step with the unsaved rules', async () => {
    const user = userEvent.setup()
    renderPage()
    const result = await screen.findByRole('status', {
      name: 'Simulated price',
    })
    expect(result).toHaveTextContent('$3')
    expect(result).toHaveTextContent('$12')

    await user.click(screen.getByLabelText('User group'))
    await user.click(await screen.findByRole('option', { name: 'vip' }))
    await user.click(screen.getByLabelText('Token group'))
    await user.click(
      await screen.findByRole('option', { name: 'auto (auto groups)' })
    )
    const steps = screen.getByRole('list', {
      name: 'How the price is worked out',
    })
    expect(within(result).getByText('$0.9')).toBeVisible()
    expect(steps).toHaveTextContent(
      'Auto tries premium and uses the first with an enabled channel for chat-model: premium.'
    )
    expect(steps).toHaveTextContent(
      'An override for vip users billed as premium sets ×0.3 instead of the group ratio ×0.5.'
    )
  })

  it('opens the section of an invalid limit and focuses it on save', async () => {
    const user = userEvent.setup()
    const { put } = renderPage()
    await screen.findByRole('table', { name: 'Groups' })
    await user.click(
      screen.getByRole('button', { name: /Maximum custom groups per token/ })
    )
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Maximum custom groups per token',
      }),
      { target: { value: '0' } }
    )
    await user.click(
      screen.getByRole('button', { name: /Maximum custom groups per token/ })
    )
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum custom groups per token',
    })
    await waitFor(() => expect(input).toHaveFocus())
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(put).not.toHaveBeenCalled()
  })

  it('reorders auto groups and keeps typed top-up decimals', async () => {
    const user = userEvent.setup()
    const { put } = renderPage()
    await screen.findByRole('table', { name: 'Groups' })

    await user.click(
      screen.getByRole('button', { name: /Auto groups and their order/ })
    )
    await user.click(screen.getByRole('button', { name: 'Move default up' }))
    const list = screen.getByRole('list', { name: 'Auto group order' })
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent(
      'default'
    )

    await user.click(
      screen.getByRole('button', { name: /Top-up group ratios/ })
    )
    const input = screen.getByRole('spinbutton', {
      name: 'Top-up ratio for premium',
    })
    await user.type(input, '0.04')
    expect(input).toHaveValue(0.04)
    await user.tab()

    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(put.mock.calls.map((call) => call[1])).toEqual([
      { key: 'AutoGroups', value: '["default","premium"]' },
      { key: 'TopupGroupRatio', value: '{"vip":1.2,"premium":0.04}' },
    ])
  })
})
