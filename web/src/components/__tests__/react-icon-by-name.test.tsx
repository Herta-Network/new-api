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
import { render, screen, waitFor } from '@testing-library/react'
import { createElement } from 'react'
import { expect, it } from 'vitest'

import { ReactIconByName } from '../react-icon-by-name'

const cases = [
  {
    name: 'Gi3dGlasses',
    load: async () => (await import('react-icons/gi')).Gi3dGlasses,
  },
  { name: 'FaBeer', load: async () => (await import('react-icons/fa')).FaBeer },
  {
    name: 'FaAnchor',
    load: async () => (await import('react-icons/fa6')).FaAnchor,
  },
  {
    name: 'HiHome',
    load: async () => (await import('react-icons/hi2')).HiHome,
  },
  {
    name: 'FcGoogle',
    load: async () => (await import('react-icons/fc')).FcGoogle,
  },
]
it.each(cases)(
  'preserves the SVG and props of $name across data chunks',
  async (testCase) => {
    const props = {
      size: 29,
      color: 'rebeccapurple',
      title: 'Named icon',
      role: 'img',
      'aria-label': 'named icon',
      'aria-hidden': false,
    }
    const Original = await testCase.load()
    const original = render(createElement(Original, props))
    const expected = original.container.innerHTML
    original.unmount()
    render(<ReactIconByName name={testCase.name} {...props} />)
    const actual = await screen.findByRole('img', { name: 'named icon' })
    expect(actual.outerHTML).toBe(expected)
  }
)
it('replaces an icon when its name changes and keeps unknown names empty', async () => {
  const view = render(
    <ReactIconByName name='FcGoogle' role='img' aria-label='changing icon' />
  )
  const initial = (await screen.findByRole('img', { name: 'changing icon' }))
    .innerHTML
  view.rerender(
    <ReactIconByName name='FaBeer' role='img' aria-label='changing icon' />
  )
  await waitFor(() =>
    expect(
      screen.getByRole('img', { name: 'changing icon' }).innerHTML
    ).not.toBe(initial)
  )
  view.rerender(
    <ReactIconByName name='GiNotAnIcon' role='img' aria-label='changing icon' />
  )
  expect(
    screen.queryByRole('img', { name: 'changing icon' })
  ).not.toBeInTheDocument()
})
