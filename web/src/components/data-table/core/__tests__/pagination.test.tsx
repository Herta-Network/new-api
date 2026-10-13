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
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { DataTablePagination } from '../pagination'

const rows = [{ id: 1 }, { id: 2 }, { id: 3 }]
const emptyRows: { id: number }[] = []

function Fixture(props: { empty?: boolean; compact?: boolean }) {
  const table = useReactTable({
    data: props.empty ? emptyRows : rows,
    columns: [{ accessorKey: 'id' }],
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageIndex: 0, pageSize: 2 } },
  })
  return <DataTablePagination table={table} compact={props.compact} />
}
it('moves between pages in compact mode and disables the boundary actions', async () => {
  const user = userEvent.setup()
  render(<Fixture compact />)
  expect(screen.getByText('1 / 2')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  expect(screen.getByText('2 / 2')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to previous page' }))
  expect(screen.getByText('1 / 2')).toBeVisible()
})
it('shows a valid empty page with navigation disabled', () => {
  render(<Fixture empty compact />)
  expect(screen.getByText('1 / 1')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
})
it('keeps page size selection available in the default layout', () => {
  render(<Fixture />)
  expect(screen.getByRole('combobox')).toBeVisible()
})

function CursorFixture(props: {
  page: number
  next: boolean
  previous: boolean
  busy?: boolean
  compact?: boolean
  onChange?: (page: number) => void
}) {
  const table = useReactTable({
    data: rows,
    columns: [{ accessorKey: 'id' }],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    pageCount: -1,
    state: { pagination: { pageIndex: props.page, pageSize: 20 } },
    onPaginationChange: (updater) => {
      const next =
        typeof updater === 'function'
          ? updater({ pageIndex: props.page, pageSize: 20 })
          : updater
      props.onChange?.(next.pageIndex)
    },
  })
  return (
    <DataTablePagination
      table={table}
      compact={props.compact}
      cursor={{
        hasNextPage: props.next,
        hasPreviousPage: props.previous,
        isFetching: props.busy ?? false,
      }}
    />
  )
}

it('uses cursor boundaries without displaying an invented total or last page', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const view = render(
    <CursorFixture page={0} next previous={false} onChange={onChange} />
  )
  expect(screen.queryByText('Total:')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Go to last page' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  expect(onChange).toHaveBeenLastCalledWith(1)
  view.rerender(
    <CursorFixture page={1} next={false} previous onChange={onChange} />
  )
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to previous page' }))
  expect(onChange).toHaveBeenLastCalledWith(0)
})

it('disables cursor navigation while a page request is pending on mobile', () => {
  render(<CursorFixture page={2} next previous busy compact />)
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  expect(screen.queryByText('Total:')).not.toBeInTheDocument()
})
