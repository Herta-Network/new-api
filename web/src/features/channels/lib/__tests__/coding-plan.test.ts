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
import { describe, expect, test } from 'vitest'

import type { CodingPlanProvider } from '../../api'
import {
  CHANNEL_TYPE_ANTHROPIC,
  CHANNEL_TYPE_MINIMAX,
  CHANNEL_TYPE_MOONSHOT,
  CHANNEL_TYPE_ZHIPU_V4,
} from '../../constants'
import { getCodingPlanProvider } from '../coding-plan'

// The same cases as TestDetect in relay/channel/codingplan, so the balance
// column offers plan usage exactly for the channels the server answers.
const cases: [string, number, string | null, CodingPlanProvider | null][] = [
  [
    'moonshot with the kimi-coding-plan base',
    CHANNEL_TYPE_MOONSHOT,
    'kimi-coding-plan',
    'kimi',
  ],
  [
    'moonshot on the Kimi coding endpoint',
    CHANNEL_TYPE_MOONSHOT,
    'https://api.kimi.com/coding/',
    'kimi',
  ],
  [
    'anthropic on the Kimi coding endpoint',
    CHANNEL_TYPE_ANTHROPIC,
    'https://api.kimi.ai/coding',
    'kimi',
  ],
  [
    'moonshot open platform',
    CHANNEL_TYPE_MOONSHOT,
    'https://api.moonshot.cn',
    null,
  ],
  ['moonshot default base', CHANNEL_TYPE_MOONSHOT, null, null],
  [
    'a kimi host outside /coding',
    CHANNEL_TYPE_ANTHROPIC,
    'https://api.kimi.com/codingx',
    null,
  ],
  [
    'zhipu with the glm-coding-plan base',
    CHANNEL_TYPE_ZHIPU_V4,
    'glm-coding-plan',
    'glm',
  ],
  [
    'zhipu with the international plan base',
    CHANNEL_TYPE_ZHIPU_V4,
    'glm-coding-plan-international',
    'glm',
  ],
  [
    'zhipu on the coding chat endpoint',
    CHANNEL_TYPE_ZHIPU_V4,
    'https://open.bigmodel.cn/api/coding/paas/v4',
    'glm',
  ],
  [
    'anthropic on the GLM Claude Code endpoint',
    CHANNEL_TYPE_ANTHROPIC,
    'https://api.z.ai/api/anthropic',
    'glm',
  ],
  [
    'zhipu open platform',
    CHANNEL_TYPE_ZHIPU_V4,
    'https://open.bigmodel.cn',
    null,
  ],
  ['minimax default base', CHANNEL_TYPE_MINIMAX, '', 'minimax'],
  [
    'minimax on the CN inference host',
    CHANNEL_TYPE_MINIMAX,
    'https://api.minimax.cn',
    'minimax',
  ],
  [
    'anthropic on the MiniMax international host',
    CHANNEL_TYPE_ANTHROPIC,
    'https://api.minimax.io/anthropic',
    'minimax',
  ],
  [
    'minimax behind a proxy',
    CHANNEL_TYPE_MINIMAX,
    'https://proxy.example.com',
    null,
  ],
  [
    'a plan endpoint on a channel type that does not use it',
    1,
    'https://api.kimi.com/coding',
    null,
  ],
  [
    'kimi coding endpoint on the zhipu type',
    CHANNEL_TYPE_ZHIPU_V4,
    'https://api.kimi.com/coding',
    null,
  ],
  [
    'a lookalike domain',
    CHANNEL_TYPE_ANTHROPIC,
    'https://api.notkimi.com/coding',
    null,
  ],
  ['anthropic official', CHANNEL_TYPE_ANTHROPIC, '', null],
  ['doubao coding plan has no usage route', 45, 'doubao-coding-plan', null],
]

describe('getCodingPlanProvider', () => {
  test.each(cases)('%s', (_name, type, baseUrl, expected) => {
    expect(getCodingPlanProvider(type, baseUrl)).toBe(expected)
  })
})
