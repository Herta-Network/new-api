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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { PAYMENT_ICONS } from '@/features/wallet/lib/payment-icons'
import { api } from '@/lib/api'

import type {
  PaymentMethod,
  PaymentPlatformsInfo,
} from '../payment-methods-api'
import { PaymentMethodsSection } from '../payment-methods-section'

const platforms: PaymentPlatformsInfo = {
  currencies: ['USD', 'CNY'],
  exchange_rate_api_key_configured: false,
  platforms: [
    {
      platform: 'epay',
      name: 'EPay',
      url: '',
      fields: [
        {
          key: 'url',
          label: 'Gateway URL',
          required: true,
          secret: false,
          managed: false,
        },
        {
          key: 'pid',
          label: 'Merchant ID',
          required: true,
          secret: false,
          managed: false,
        },
        {
          key: 'version',
          label: 'Protocol version',
          type: 'select',
          required: true,
          secret: false,
          managed: false,
          options: [
            { value: 'v2', label: 'EPay V2 (RSA keys)' },
            { value: 'v1', label: 'EPay V1 (MD5 merchant key)' },
          ],
        },
        {
          key: 'checkout',
          label: 'Checkout mode',
          description:
            'An API order is created by the server, which notices a failing gateway before the buyer leaves.',
          type: 'select',
          required: true,
          secret: false,
          managed: false,
          options: [
            { value: 'api', label: 'API order' },
            { value: 'page', label: 'Gateway payment page' },
          ],
        },
        {
          key: 'key',
          label: 'Merchant key',
          required: true,
          secret: true,
          managed: false,
          when: { version: 'v1' },
        },
        {
          key: 'private_key',
          label: 'Merchant private key',
          required: true,
          secret: true,
          managed: false,
          when: { version: 'v2' },
        },
        {
          key: 'platform_public_key',
          label: 'Platform public key',
          type: 'textarea',
          required: true,
          secret: false,
          managed: false,
          when: { version: 'v2' },
        },
        {
          key: 'type',
          label: 'Payment type',
          required: false,
          secret: false,
          managed: false,
        },
      ],
    },
    {
      platform: 'stripe',
      name: 'Stripe',
      url: 'https://stripe.com',
      fields: [
        {
          key: 'public_key',
          label: 'Publishable key',
          required: true,
          secret: false,
          managed: false,
        },
        {
          key: 'secret_key',
          label: 'Secret key',
          required: true,
          secret: true,
          managed: false,
        },
        {
          key: 'payment_method_type',
          label: 'Payment method type',
          type: 'select',
          required: true,
          secret: false,
          managed: false,
          options: [
            { value: 'card', label: 'Card' },
            { value: 'alipay', label: 'Alipay' },
            { value: 'wechat_pay', label: 'WeChat Pay' },
          ],
        },
        {
          key: 'webhook_url',
          label: 'Webhook URL',
          required: false,
          secret: false,
          managed: true,
        },
      ],
    },
  ],
}

function method(overrides: Partial<PaymentMethod>): PaymentMethod {
  return {
    id: 1,
    name: 'Alipay',
    platform: 'epay',
    icon: '',
    description: '',
    sort: 0,
    enabled: true,
    domain: '',
    fee_mode: 0,
    fee_rate_bp: 0,
    fee_fixed: 0,
    created_at: 0,
    updated_at: 0,
    config: {},
    secrets_set: {},
    notify_url: '',
    webhook_stale: false,
    ...overrides,
  }
}

const balance = method({ id: -1, name: 'Balance', platform: 'balance' })
const epay = method({
  id: 2,
  name: 'Alipay',
  config: {
    url: 'https://pay.example.com',
    pid: '1001',
    version: 'v1',
    checkout: 'page',
    key: '',
    type: 'alipay',
  },
  secrets_set: { key: true },
  notify_url: 'https://api.example.com/api/payment/notify/epay/token-2',
})
const stripe = method({
  id: 3,
  name: 'Card',
  platform: 'stripe',
  config: {
    public_key: 'pk_test_1',
    secret_key: '',
    payment_method_type: 'wechat_pay',
    webhook_url: 'https://old.example.com/hook',
  },
  secrets_set: { secret_key: true },
  notify_url: 'https://api.example.com/api/payment/notify/stripe/token-3',
  webhook_stale: true,
})

function mockApi(methods: PaymentMethod[]) {
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === '/api/payment/settings/platforms') {
      return { data: { success: true, data: platforms } }
    }
    return { data: { success: true, data: methods } }
  })
  const write = { data: { success: true, data: methods[0] } }
  return {
    post: vi.spyOn(api, 'post').mockResolvedValue(write),
    put: vi.spyOn(api, 'put').mockResolvedValue(write),
    del: vi.spyOn(api, 'delete').mockResolvedValue(write),
  }
}

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <PaymentMethodsSection currency='USD' />
    </QueryClientProvider>
  )
  return userEvent.setup()
}

afterEach(() => vi.restoreAllMocks())

describe('payment methods', () => {
  it('creates a method with the fee percentage in basis points and the fixed fee in hundredths', async () => {
    const request = mockApi([balance])
    const user = renderSection()
    await user.click(
      await screen.findByRole('button', { name: 'Add payment method' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Name' }),
      'Alipay'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Gateway URL' }),
      'https://pay.example.com'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Merchant ID' }),
      '1001'
    )
    await user.type(
      within(dialog).getByLabelText('Merchant private key'),
      'MIIEv-private'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Platform public key' }),
      'MIIBI-public'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Payment type' }),
      'alipay'
    )
    await user.click(
      within(dialog).getByRole('combobox', { name: 'Payment fee' })
    )
    await user.click(
      await screen.findByRole('option', { name: 'Percentage + fixed' })
    )
    await user.type(
      within(dialog).getByRole('spinbutton', { name: 'Fee rate (%)' }),
      '2.9'
    )
    await user.type(
      within(dialog).getByRole('spinbutton', { name: 'Fixed fee (USD)' }),
      '.3'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.post).toHaveBeenCalledTimes(1))
    expect(request.post).toHaveBeenCalledWith(
      '/api/payment/settings/methods',
      expect.objectContaining({
        name: 'Alipay',
        platform: 'epay',
        config: {
          url: 'https://pay.example.com',
          pid: '1001',
          version: 'v2',
          checkout: 'api',
          private_key: 'MIIEv-private',
          platform_public_key: 'MIIBI-public',
          type: 'alipay',
        },
        fee_mode: 3,
        fee_rate_bp: 290,
        fee_fixed: 30,
      })
    )
  })

  it('requires a secret that is not stored yet before creating a method', async () => {
    const request = mockApi([balance])
    const user = renderSection()
    await user.click(
      await screen.findByRole('button', { name: 'Add payment method' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Name' }),
      'Alipay'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Gateway URL' }),
      'https://pay.example.com'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Merchant ID' }),
      '1001'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    expect(
      await within(dialog).findAllByText('This field is required')
    ).toHaveLength(2)
    expect(
      within(dialog).getByLabelText('Merchant private key')
    ).toHaveAttribute('aria-invalid', 'true')
    expect(
      within(dialog).getByRole('textbox', { name: 'Platform public key' })
    ).toHaveAttribute('aria-invalid', 'true')
    expect(request.post).not.toHaveBeenCalled()
  })

  it('keeps a stored secret when its field is left blank on edit', async () => {
    const request = mockApi([balance, epay])
    const user = renderSection()
    await user.click(
      within(await screen.findByRole('row', { name: /Alipay/ })).getByRole(
        'button',
        { name: 'Edit' }
      )
    )
    const dialog = await screen.findByRole('dialog')
    const secret = within(dialog).getByLabelText(/Merchant key/)
    expect(secret).toHaveValue('')
    expect(secret).toHaveAttribute('type', 'password')
    expect(within(dialog).getByText('Configured')).toBeVisible()
    const name = within(dialog).getByRole('textbox', { name: 'Name' })
    await user.clear(name)
    await user.type(name, 'Alipay CN')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/2',
      expect.objectContaining({
        name: 'Alipay CN',
        config: {
          url: 'https://pay.example.com',
          pid: '1001',
          version: 'v1',
          checkout: 'page',
          key: '',
          type: 'alipay',
        },
      })
    )
  })

  it('starts a new EPay method on V2 API orders and asks for the merchant key instead of the RSA keys once V1 is chosen', async () => {
    const request = mockApi([balance])
    const user = renderSection()
    await user.click(
      await screen.findByRole('button', { name: 'Add payment method' })
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByRole('combobox', { name: 'Protocol version' })
    ).toHaveTextContent('EPay V2 (RSA keys)')
    expect(
      within(dialog).getByRole('combobox', { name: 'Checkout mode' })
    ).toHaveTextContent('API order')
    expect(
      within(dialog).getByText(
        'An API order is created by the server, which notices a failing gateway before the buyer leaves.'
      )
    ).toBeVisible()
    expect(
      within(dialog).getByLabelText('Merchant private key')
    ).toHaveAttribute('type', 'password')
    expect(
      within(dialog).queryByLabelText('Merchant key')
    ).not.toBeInTheDocument()

    await user.type(
      within(dialog).getByRole('textbox', { name: 'Name' }),
      'Alipay'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Gateway URL' }),
      'https://pay.example.com'
    )
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Merchant ID' }),
      '1001'
    )
    await user.type(
      within(dialog).getByLabelText('Merchant private key'),
      'MIIEv-private'
    )
    await user.click(
      within(dialog).getByRole('combobox', { name: 'Protocol version' })
    )
    await user.click(
      await screen.findByRole('option', { name: 'EPay V1 (MD5 merchant key)' })
    )

    expect(
      within(dialog).queryByLabelText('Merchant private key')
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByRole('textbox', { name: 'Platform public key' })
    ).not.toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('Merchant key'), 'v1-secret')
    await user.type(
      within(dialog).getByRole('textbox', { name: 'Payment type' }),
      'alipay'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.post).toHaveBeenCalledTimes(1))
    expect(request.post).toHaveBeenCalledWith(
      '/api/payment/settings/methods',
      expect.objectContaining({
        config: {
          url: 'https://pay.example.com',
          pid: '1001',
          version: 'v1',
          checkout: 'api',
          key: 'v1-secret',
          type: 'alipay',
        },
      })
    )
  })

  it('lets the balance method be disabled but never deleted', async () => {
    const request = mockApi([balance, epay])
    const user = renderSection()
    await user.click(
      within(await screen.findByRole('row', { name: /Balance/ })).getByRole(
        'button',
        { name: 'Open menu' }
      )
    )
    expect(
      screen.queryByRole('menuitem', { name: 'Delete' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Disable' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/-1',
      expect.objectContaining({ name: 'Balance', enabled: false })
    )
  })

  it('shows the payment method type of a Stripe method and saves another one chosen from the platform options', async () => {
    const request = mockApi([balance, stripe])
    const user = renderSection()
    const row = within(await screen.findByRole('row', { name: /Card/ }))
    expect(row.getByText('WeChat Pay')).toBeVisible()
    await user.click(row.getByRole('button', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    const paymentType = within(dialog).getByRole('combobox', {
      name: 'Payment method type',
    })
    expect(paymentType).toHaveTextContent('WeChat Pay')
    await user.click(paymentType)
    await user.click(await screen.findByRole('option', { name: 'Alipay' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/3',
      expect.objectContaining({
        config: {
          public_key: 'pk_test_1',
          secret_key: '',
          payment_method_type: 'alipay',
        },
      })
    )
  })

  it('starts a new Stripe method on the first payment method type', async () => {
    mockApi([balance])
    const user = renderSection()
    await user.click(
      await screen.findByRole('button', { name: 'Add payment method' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('combobox', { name: 'Platform' }))
    await user.click(await screen.findByRole('option', { name: 'Stripe' }))

    expect(
      within(dialog).getByRole('combobox', { name: 'Payment method type' })
    ).toHaveTextContent('Card')
  })

  it('lists the curated payment icons with their icons and saves the name of the chosen one', async () => {
    const request = mockApi([balance, epay])
    const user = renderSection()
    await user.click(
      within(await screen.findByRole('row', { name: /Alipay/ })).getByRole(
        'button',
        { name: 'Edit' }
      )
    )
    const dialog = await screen.findByRole('dialog')
    const picker = within(dialog).getByRole('combobox', { name: 'Icon' })
    expect(picker).toHaveValue('Platform icon')
    await user.click(picker)

    const options = await screen.findAllByRole('option')
    expect(options.map((option) => option.textContent)).toEqual([
      'Platform icon',
      ...PAYMENT_ICONS.map((icon) => icon.labelKey),
    ])
    for (const option of options.slice(1)) {
      expect(option.querySelector('svg')).toBeInTheDocument()
    }
    await user.click(screen.getByRole('option', { name: 'Tether (USDT)' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/2',
      expect.objectContaining({ icon: 'SiTether' })
    )
  })

  it('shows a stored curated icon in its row and picker and clears it to the platform icon', async () => {
    const request = mockApi([balance, { ...epay, icon: 'SiAlipay' }])
    const user = renderSection()
    const row = within(await screen.findByRole('row', { name: /Alipay/ }))
    expect(
      row.getByRole('cell', { name: 'Alipay' }).querySelector('svg')
    ).toBeInTheDocument()
    await user.click(row.getByRole('button', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    const picker = within(dialog).getByRole('combobox', { name: 'Icon' })
    expect(picker).toHaveValue('Alipay')
    await user.click(picker)
    await user.click(
      await screen.findByRole('option', { name: 'Platform icon' })
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/2',
      expect.objectContaining({ icon: '' })
    )
  })

  it('keeps a stored icon name outside the curated list when the picker is left alone', async () => {
    const request = mockApi([balance, { ...epay, icon: 'GiTwoCoins' }])
    const user = renderSection()
    const row = within(await screen.findByRole('row', { name: /Alipay/ }))
    expect(
      row.getByRole('cell', { name: 'Alipay' }).querySelector('svg')
    ).toBeNull()
    await user.click(row.getByRole('button', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('combobox', { name: 'Icon' })).toHaveValue(
      'GiTwoCoins'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(request.put).toHaveBeenCalledTimes(1))
    expect(request.put).toHaveBeenCalledWith(
      '/api/payment/settings/methods/2',
      expect.objectContaining({ icon: 'GiTwoCoins' })
    )
  })

  it('shows the callback URL and lets a stale Stripe webhook be synced', async () => {
    const request = mockApi([balance, stripe])
    const user = renderSection()
    const row = within(await screen.findByRole('row', { name: /Card/ }))
    expect(row.getByText(stripe.notify_url)).toBeVisible()
    expect(row.getByRole('button', { name: 'Copy callback URL' })).toBeVisible()
    expect(row.getByText('Webhook outdated')).toBeVisible()
    await user.click(row.getByRole('button', { name: 'Open menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sync webhook' }))

    await waitFor(() =>
      expect(request.post).toHaveBeenCalledWith(
        '/api/payment/settings/methods/3/webhook/sync'
      )
    )
  })
})
