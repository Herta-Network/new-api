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
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { RuleEditorDialog } from '../rule-editor-dialog'
import type { AffinityRule } from '../types'

const conversationRule: AffinityRule = {
  name: 'opencode go session',
  model_regex: ['^glm-.*$'],
  path_regex: [],
  key_sources: [{ type: 'conversation_session' }],
  ttl_seconds: 0,
  skip_retry_on_failure: false,
  include_using_group: true,
  include_model_name: false,
  include_rule_name: true,
}

test('a conversation_session key source needs no key and is saved as is', async () => {
  const onSave = vi.fn()
  render(
    <RuleEditorDialog
      open
      onOpenChange={() => {}}
      rule={conversationRule}
      onSave={onSave}
    />
  )

  expect(
    await screen.findByText(
      'Uses the conversation recognized from the request history; no key is needed'
    )
  ).toBeInTheDocument()
  expect(screen.queryByPlaceholderText('user_id')).not.toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
  expect(onSave.mock.calls[0][0].key_sources).toEqual([
    { type: 'conversation_session', key: '', path: '' },
  ])
})
