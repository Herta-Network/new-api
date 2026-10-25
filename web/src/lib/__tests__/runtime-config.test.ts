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
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '../http-client'
import {
  RUNTIME_CONFIG_PATH,
  apiOrigin,
  apiUrl,
  getApiBaseUrl,
  loadRuntimeConfig,
  parseApiBaseUrl,
  setApiBaseUrl,
} from '../runtime-config'

const originalAdapter = api.defaults.adapter

afterEach(() => {
  setApiBaseUrl('')
  api.defaults.adapter = originalAdapter
})

function respond(body: string, status = 200): typeof fetch {
  return vi.fn(async () => new Response(body, { status }))
}

describe('runtime backend address', () => {
  test.each([
    ['https://api.example.com', 'https://api.example.com'],
    ['https://api.example.com/', 'https://api.example.com'],
    ['  https://API.example.com:8443/ ', 'https://api.example.com:8443'],
    ['https://example.com/new-api/', 'https://example.com/new-api'],
    ['http://localhost:3000', 'http://localhost:3000'],
  ])('accepts the backend address %s', (value, expected) => {
    expect(parseApiBaseUrl(value)).toBe(expected)
  })

  test.each([
    '',
    'api.example.com',
    'ftp://api.example.com',
    'javascript:alert(1)',
    'https://user:secret@api.example.com',
    'https://api.example.com?next=1',
    'https://api.example.com#top',
    42,
    null,
  ])('falls back to same-origin requests for %s', (value) => {
    expect(parseApiBaseUrl(value)).toBe('')
  })

  test('the configuration file sets the backend of every request', async () => {
    const fetcher = respond('{"apiBaseUrl":"https://api.example.com/"}')
    await loadRuntimeConfig(fetcher)

    expect(fetcher).toHaveBeenCalledWith(RUNTIME_CONFIG_PATH, {
      cache: 'no-store',
    })
    expect(getApiBaseUrl()).toBe('https://api.example.com')
    expect(apiUrl('/api/status')).toBe('https://api.example.com/api/status')
    expect(apiOrigin()).toBe('https://api.example.com')

    const adapter = vi.fn(async (config) => ({
      data: { success: true },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }))
    api.defaults.adapter = adapter
    await api.get('/api/status', { disableDuplicate: true })
    const [config] = adapter.mock.calls[0]
    expect(config.baseURL).toBe('https://api.example.com')
    expect(config.withCredentials).toBeFalsy()
  })

  test.each([
    [
      'an index.html fallback',
      respond('<!doctype html><title>New API</title>'),
    ],
    ['a missing file', respond('not found', 404)],
    ['an empty address', respond('{"apiBaseUrl":""}')],
    ['an invalid address', respond('{"apiBaseUrl":"api.example.com"}')],
  ])('%s keeps requests on the page origin', async (_name, fetcher) => {
    await loadRuntimeConfig(fetcher)

    expect(getApiBaseUrl()).toBe('')
    expect(apiUrl('/api/status')).toBe('/api/status')
    expect(apiOrigin()).toBe(window.location.origin)
  })
})
