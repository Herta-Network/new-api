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
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import { CompleteStep } from '../components/complete-step'
import { DatabaseStep } from '../components/database-step'

const status = {
  status: false,
  root_init: false,
  database_type: 'postgres' as const,
}

test('PostgreSQL setup shows its database guidance and completion label', () => {
  const database = render(<DatabaseStep status={status} />)
  expect(screen.getByRole('alert')).toHaveTextContent('PostgreSQL detected')
  expect(screen.getAllByText('PostgreSQL')).toHaveLength(2)
  database.unmount()

  render(
    <CompleteStep
      status={status}
      values={{
        username: 'admin',
        password: '',
        confirmPassword: '',
        usageMode: 'self',
      }}
    />
  )
  expect(screen.getAllByText('PostgreSQL')).toHaveLength(2)
})

test('Pending setup status shows an unknown database until detection completes', () => {
  render(<DatabaseStep />)
  expect(screen.getAllByText('Unknown')).toHaveLength(2)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
