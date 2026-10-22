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
import { describe, expect, it } from 'vitest'

import type { PricingModel } from '@/features/pricing/types'

// The same cases are asserted by service/group_auto_groups_test.go against
// the gateway's own functions.
import cases from '../../../../../../../service/testdata/group_rules_cases.json'
import {
  groupOptionUpdates,
  parseGroupOptions,
  removeGroup,
  writeGroup,
  type GroupPricingValues,
} from '../lib/group-options'
import {
  resolveAutoGroups,
  resolveGroupRatio,
  resolveUsableGroups,
  simulateGroupCall,
  topupDue,
  type GroupRules,
} from '../lib/group-rules'
import { modelBasePrice, withGroupRatio } from '../lib/simulated-price'

const rules: GroupRules = {
  groupRatio: cases.settings.GroupRatio,
  userUsableGroups: cases.settings.UserUsableGroups,
  groupGroupRatio: cases.settings.GroupGroupRatio,
  specialUsableGroups: cases.settings.GroupSpecialUsableGroup,
  autoGroups: cases.settings.AutoGroups,
}

function pricingModel(overrides: Partial<PricingModel>): PricingModel {
  return {
    id: 0,
    model_name: 'chat-model',
    quota_type: 0,
    model_ratio: 1.5,
    completion_ratio: 4,
    enable_groups: [],
    ...overrides,
  }
}

describe('group rules shared with the gateway', () => {
  it('resolves usable groups like service.GetUserUsableGroups', () => {
    for (const [userGroup, expected] of Object.entries(cases.usable_groups)) {
      expect([...resolveUsableGroups(rules, userGroup).keys()].sort()).toEqual(
        expected
      )
    }
  })

  it('resolves auto groups like service.GetUserAutoGroup', () => {
    for (const [userGroup, expected] of Object.entries(cases.auto_groups)) {
      expect(resolveAutoGroups(rules, userGroup)).toEqual(expected)
    }
  })

  it('resolves ratios like service.GetUserGroupRatio', () => {
    for (const item of cases.group_ratios) {
      expect(
        resolveGroupRatio(rules, item.user_group, item.using_group).ratio
      ).toBe(item.ratio)
    }
  })
})

describe('price simulator', () => {
  it('bills a token without a group at the user group ratio', () => {
    const result = simulateGroupCall(rules, {
      userGroup: 'vip',
      tokenGroup: '',
      modelGroups: ['vip'],
    })
    expect(result).toMatchObject({
      status: 'ok',
      billingGroup: 'vip',
      ratio: 0.8,
      ratioSource: 'group',
      hasChannel: true,
    })
  })

  it('applies an inter-group override instead of the billing group ratio', () => {
    const result = simulateGroupCall(rules, {
      userGroup: 'vip',
      tokenGroup: 'premium',
      modelGroups: ['premium'],
    })
    expect(result).toMatchObject({
      status: 'ok',
      billingGroup: 'premium',
      ratio: 0.3,
      ratioSource: 'override',
      groupRatio: 0.5,
    })
    const price = withGroupRatio(
      modelBasePrice(pricingModel({ enable_groups: ['premium'] })),
      0.3
    )
    expect(price).toEqual({
      kind: 'token',
      input: expect.closeTo(0.9, 10),
      output: expect.closeTo(3.6, 10),
    })
  })

  it('picks the first auto group that has a channel for the model', () => {
    const result = simulateGroupCall(rules, {
      userGroup: 'vip',
      tokenGroup: 'auto',
      modelGroups: ['partner', 'default'],
    })
    expect(result).toMatchObject({
      status: 'ok',
      usingGroup: 'auto',
      billingGroup: 'partner',
      ratio: 2,
      autoCandidates: [
        { group: 'premium', hasChannel: false },
        { group: 'partner', hasChannel: true },
      ],
    })
    expect(
      simulateGroupCall(rules, {
        userGroup: 'vip',
        tokenGroup: 'auto',
        modelGroups: ['default'],
      })
    ).toEqual({
      status: 'no_channel',
      candidates: [
        { group: 'premium', hasChannel: false },
        { group: 'partner', hasChannel: false },
      ],
    })
  })

  it('follows special usable rules when checking the token group', () => {
    expect(
      simulateGroupCall(rules, { userGroup: 'vip', tokenGroup: 'partner' })
    ).toMatchObject({ status: 'ok', billingGroup: 'partner', ratio: 2 })
    expect(
      simulateGroupCall(rules, { userGroup: 'vip', tokenGroup: 'default' })
    ).toEqual({
      status: 'rejected',
      reason: 'not_usable',
      tokenGroup: 'default',
    })
    expect(
      simulateGroupCall(rules, { userGroup: 'default', tokenGroup: 'retired' })
    ).toEqual({
      status: 'rejected',
      reason: 'not_priced',
      tokenGroup: 'retired',
    })
  })

  it('rejects auto when the user has no auto group to try', () => {
    expect(
      simulateGroupCall(
        { ...rules, autoGroups: ['retired'] },
        { userGroup: 'default', tokenGroup: 'auto' }
      )
    ).toEqual({ status: 'no_auto_groups' })
  })

  it('multiplies a per-request price by the ratio', () => {
    const price = modelBasePrice(
      pricingModel({ quota_type: 1, model_price: 0.04 })
    )
    expect(withGroupRatio(price, 0.5)).toEqual({
      kind: 'request',
      price: 0.02,
    })
  })

  it('charges top-ups at the user group ratio, treating a missing ratio as 1', () => {
    expect(topupDue(10_000, 1.2)).toBe(12_000)
    expect(topupDue(10_000, undefined)).toBe(10_000)
    expect(topupDue(10_000, 0)).toBe(10_000)
  })
})

const saved: GroupPricingValues = {
  GroupRatio: '{"default":1,"vip":0.8}',
  UserUsableGroups: '{"default":"Standard","auto":"Auto"}',
  GroupGroupRatio: '{"vip":{"default":0.9}}',
  AutoGroups: '["default","vip"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{"vip":{"default":"kept as written"}}',
  TopupGroupRatio: '{"vip":1.2}',
}

describe('group option edits', () => {
  it('writes one group and keeps every other entry, including groups without a ratio', () => {
    const parsed = parseGroupOptions(saved)
    const next = writeGroup(parsed.rules, {
      name: 'vip',
      ratio: 0.7,
      selectable: true,
      description: 'VIP',
    })
    expect(JSON.parse(next.GroupRatio)).toEqual({ default: 1, vip: 0.7 })
    expect(Object.keys(JSON.parse(next.UserUsableGroups))).toEqual([
      'default',
      'auto',
      'vip',
    ])
    const removed = removeGroup(parsed.rules, 'default')
    expect(JSON.parse(removed.GroupRatio)).toEqual({ vip: 0.8 })
    expect(JSON.parse(removed.UserUsableGroups)).toEqual({ auto: 'Auto' })
  })

  it('sends only the options whose meaning changed', () => {
    const reordered = {
      ...saved,
      GroupRatio: '{\n  "vip": 0.8,\n  "default": 1\n}',
      UserUsableGroups: '{"default":"Standard","auto":"Auto","vip":"VIP"}',
      MaxTokenAutoGroups: 3,
    }
    expect(groupOptionUpdates(saved, reordered)).toEqual([
      {
        field: 'UserUsableGroups',
        key: 'UserUsableGroups',
        value: '{"default":"Standard","auto":"Auto","vip":"VIP"}',
      },
      { field: 'MaxTokenAutoGroups', key: 'MaxTokenAutoGroups', value: 3 },
    ])
  })

  it('reports an option that does not parse instead of reading it as empty rules', () => {
    const parsed = parseGroupOptions({
      ...saved,
      GroupGroupRatio: '{"vip": {"default": 0.9',
      AutoGroups: '["default", 1]',
    })
    expect([...parsed.invalid]).toEqual(['GroupGroupRatio', 'AutoGroups'])
    expect(parsed.rules.groupRatio).toEqual({ default: 1, vip: 0.8 })
  })
})
