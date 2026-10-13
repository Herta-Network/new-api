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
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { lazy, useState } from 'react'
import { expect, it, vi } from 'vitest'

import { DeferredDialog } from '../deferred-dialog'
import { Dialog } from '../dialog'
import { Input } from '../ui/input'

it('loads on first open, allows closing while pending, and preserves a loaded draft', async () => {
  const user = userEvent.setup()
  let resolve!: (module: { default: typeof Content }) => void
  function Content(props: {
    open: boolean
    onOpenChange: (open: boolean) => void
  }) {
    const [value, setValue] = useState('')
    return (
      <Dialog
        open={props.open}
        onOpenChange={props.onOpenChange}
        title='Edit item'
      >
        <Input
          aria-label='Draft'
          value={value}
          onChange={(event) => setValue(event.target.value)}
        />
      </Dialog>
    )
  }
  const load = vi.fn(
    () =>
      new Promise<{ default: typeof Content }>((done) => {
        resolve = done
      })
  )
  const LazyContent = lazy(load)
  const onOpenChange = vi.fn()
  const view = render(
    <DeferredDialog open={false} onOpenChange={onOpenChange}>
      <LazyContent open={false} onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  expect(load).not.toHaveBeenCalled()
  view.rerender(
    <DeferredDialog open onOpenChange={onOpenChange}>
      <LazyContent open onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  expect(
    await screen.findByRole('dialog', { name: 'Loading...' })
  ).toBeVisible()
  await user.keyboard('{Escape}')
  expect(onOpenChange.mock.calls[0][0]).toBe(false)
  view.rerender(
    <DeferredDialog open={false} onOpenChange={onOpenChange}>
      <LazyContent open={false} onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  await act(async () => {
    resolve({ default: Content })
  })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  view.rerender(
    <DeferredDialog open onOpenChange={onOpenChange}>
      <LazyContent open onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  await user.type(
    await screen.findByRole('textbox', { name: 'Draft' }),
    'keep me'
  )
  view.rerender(
    <DeferredDialog open={false} onOpenChange={onOpenChange}>
      <LazyContent open={false} onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  view.rerender(
    <DeferredDialog open onOpenChange={onOpenChange}>
      <LazyContent open onOpenChange={onOpenChange} />
    </DeferredDialog>
  )
  expect(await screen.findByRole('textbox', { name: 'Draft' })).toHaveValue(
    'keep me'
  )
  expect(load).toHaveBeenCalledTimes(1)
})
