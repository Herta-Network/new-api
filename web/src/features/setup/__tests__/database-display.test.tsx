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
import { describe, expect, test } from 'vitest'

import { CompleteStep } from '../components/complete-step'
import { DatabaseStep } from '../components/database-step'
import type { SetupFormValues, SetupStatus } from '../types'

const FORM_VALUES: SetupFormValues = {
  username: 'root',
  password: '',
  confirmPassword: '',
  usageMode: 'external',
}

function statusWithDatabase(databaseType: string): SetupStatus {
  return { status: false, root_init: false, database_type: databaseType }
}

describe('DatabaseStep', () => {
  test('postgres status shows PostgreSQL as healthy with its maintenance guidance', () => {
    render(<DatabaseStep status={statusWithDatabase('postgres')} />)

    expect(screen.getByTitle('PostgreSQL')).toHaveClass('text-success')
    expect(screen.getByRole('alert')).toHaveTextContent('PostgreSQL detected')
  })

  test('another reported database shows its raw name without engine-specific guidance', () => {
    render(<DatabaseStep status={statusWithDatabase('sqlite')} />)

    expect(screen.getByTitle('sqlite')).toHaveClass('text-info')
    expect(
      screen.getByText(
        'The setup wizard will use this database during initialization.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('missing status shows Unknown without engine-specific guidance', () => {
    render(<DatabaseStep />)

    expect(screen.getByTitle('Unknown')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('CompleteStep', () => {
  test('postgres status marks the database as healthy on the review step', () => {
    render(
      <CompleteStep
        status={statusWithDatabase('postgres')}
        values={FORM_VALUES}
      />
    )

    expect(screen.getByTitle('postgres')).toHaveClass('text-success')
  })

  test('another reported database is shown as neutral on the review step', () => {
    render(
      <CompleteStep
        status={statusWithDatabase('sqlite')}
        values={FORM_VALUES}
      />
    )

    expect(screen.getByTitle('sqlite')).toHaveClass('text-muted-foreground')
  })
})
