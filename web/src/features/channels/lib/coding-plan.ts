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
import type { CodingPlanProvider } from '../api'
import {
  CHANNEL_TYPE_ANTHROPIC,
  CHANNEL_TYPE_MINIMAX,
  CHANNEL_TYPE_MOONSHOT,
  CHANNEL_TYPE_ZHIPU_V4,
} from '../constants'

/** Product names of the coding plans; brand names, not translated. */
export const CODING_PLAN_NAMES: Record<CodingPlanProvider, string> = {
  kimi: 'Kimi For Coding',
  glm: 'GLM Coding Plan',
  minimax: 'MiniMax Coding Plan',
}

/** Plan base keys of constant.ChannelSpecialBases and the endpoint each stands for. */
const PLAN_BASE_URLS: Record<string, string> = {
  'kimi-coding-plan': 'https://api.kimi.com/coding',
  'glm-coding-plan': 'https://open.bigmodel.cn/api/anthropic',
  'glm-coding-plan-international': 'https://api.z.ai/api/anthropic',
}

function isOnDomain(host: string, domains: string[]): boolean {
  return domains.some(
    (domain) => host === domain || host.endsWith(`.${domain}`)
  )
}

function isUnderPath(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}/`)
}

/**
 * Recognises a Kimi For Coding, GLM Coding Plan or MiniMax Coding Plan channel
 * by its type and base URL, as codingplan.Detect does on the server:
 * Moonshot or Anthropic channels on kimi.com / kimi.ai under /coding, Zhipu or
 * Anthropic channels on bigmodel.cn / z.ai under /api/coding or /api/anthropic,
 * and MiniMax or Anthropic channels on a MiniMax host.
 */
export function getCodingPlanProvider(
  type: number,
  baseUrl: string | null | undefined
): CodingPlanProvider | null {
  const trimmed = (baseUrl ?? '').trim()
  if (trimmed === '') {
    // Of the channel types' default bases, only MiniMax's is a plan host.
    return type === CHANNEL_TYPE_MINIMAX ? 'minimax' : null
  }

  let url: URL
  try {
    url = new URL(PLAN_BASE_URLS[trimmed] ?? trimmed)
  } catch {
    return null
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return null
  }
  const host = url.hostname.toLowerCase()
  const path = url.pathname.replace(/\/+$/, '')
  const viaAnthropic = type === CHANNEL_TYPE_ANTHROPIC

  if (
    (type === CHANNEL_TYPE_MOONSHOT || viaAnthropic) &&
    isOnDomain(host, ['kimi.com', 'kimi.ai']) &&
    isUnderPath(path, '/coding')
  ) {
    return 'kimi'
  }
  if (
    (type === CHANNEL_TYPE_ZHIPU_V4 || viaAnthropic) &&
    isOnDomain(host, ['bigmodel.cn', 'z.ai']) &&
    (isUnderPath(path, '/api/coding') || isUnderPath(path, '/api/anthropic'))
  ) {
    return 'glm'
  }
  if (
    (type === CHANNEL_TYPE_MINIMAX || viaAnthropic) &&
    isOnDomain(host, [
      'minimax.io',
      'minimaxi.com',
      'minimax.cn',
      'minimax.chat',
    ])
  ) {
    return 'minimax'
  }
  return null
}
