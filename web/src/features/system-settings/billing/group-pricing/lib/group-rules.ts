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

// Group rules as the gateway applies them. Each resolver mirrors a backend
// function (named in its comment); service/testdata/group_rules_cases.json
// holds cases that the Go and the web tests both assert.

export type ParsedOption<T> = { ok: true; value: T } | { ok: false }

export type RatioMap = Record<string, number>
export type DescriptionMap = Record<string, string>
export type NestedRatioMap = Record<string, RatioMap>
export type SpecialUsableMap = Record<string, DescriptionMap>

export type GroupRules = {
  groupRatio: RatioMap
  userUsableGroups: DescriptionMap
  groupGroupRatio: NestedRatioMap
  specialUsableGroups: SpecialUsableMap
  autoGroups: string[]
}

export const AUTO_GROUP = 'auto'

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function parseJson(text: string): ParsedOption<unknown> {
  const trimmed = text.trim()
  if (!trimmed) return { ok: true, value: undefined }
  try {
    return { ok: true, value: JSON.parse(trimmed) }
  } catch {
    return { ok: false }
  }
}

function parseObjectOf<T>(
  text: string,
  isValue: (value: unknown) => value is T
): ParsedOption<Record<string, T>> {
  const parsed = parseJson(text)
  if (!parsed.ok) return parsed
  if (parsed.value === undefined) return { ok: true, value: {} }
  if (!isPlainObject(parsed.value)) return { ok: false }
  if (!Object.values(parsed.value).every(isValue)) return { ok: false }
  return { ok: true, value: parsed.value as Record<string, T> }
}

function isRatioMap(value: unknown): value is RatioMap {
  return isPlainObject(value) && Object.values(value).every(isFiniteNumber)
}

function isDescriptionMap(value: unknown): value is DescriptionMap {
  return (
    isPlainObject(value) &&
    Object.values(value).every((item) => typeof item === 'string')
  )
}

// Ratio maps (GroupRatio, TopupGroupRatio): group → number.
export function parseRatioMap(text: string): ParsedOption<RatioMap> {
  return parseObjectOf(text, isFiniteNumber)
}

// UserUsableGroups: group → description shown when creating a token.
export function parseDescriptionMap(
  text: string
): ParsedOption<DescriptionMap> {
  return parseObjectOf(
    text,
    (value): value is string => typeof value === 'string'
  )
}

// GroupGroupRatio: user group → { billing group → ratio }.
export function parseNestedRatioMap(
  text: string
): ParsedOption<NestedRatioMap> {
  return parseObjectOf(text, isRatioMap)
}

// group_ratio_setting.group_special_usable_group:
// user group → { "+:group" | "-:group" | "group" → description }.
export function parseSpecialUsableMap(
  text: string
): ParsedOption<SpecialUsableMap> {
  return parseObjectOf(text, isDescriptionMap)
}

// AutoGroups: ordered group names.
export function parseGroupList(text: string): ParsedOption<string[]> {
  const parsed = parseJson(text)
  if (!parsed.ok) return parsed
  if (parsed.value === undefined) return { ok: true, value: [] }
  if (
    !Array.isArray(parsed.value) ||
    !parsed.value.every((item) => typeof item === 'string')
  ) {
    return { ok: false }
  }
  return { ok: true, value: parsed.value }
}

export type SpecialUsableRule = {
  userGroup: string
  key: string
  group: string
  action: 'add' | 'remove'
  description: string
}

// The prefixes service.GetUserUsableGroups understands: "-:" removes a group,
// "+:" or no prefix adds it.
export function parseSpecialUsableKey(key: string): {
  group: string
  action: 'add' | 'remove'
} {
  if (key.startsWith('-:')) return { group: key.slice(2), action: 'remove' }
  if (key.startsWith('+:')) return { group: key.slice(2), action: 'add' }
  return { group: key, action: 'add' }
}

export function listSpecialUsableRules(
  map: SpecialUsableMap
): SpecialUsableRule[] {
  return Object.entries(map).flatMap(([userGroup, rules]) =>
    Object.entries(rules).map(([key, description]) => ({
      userGroup,
      key,
      description,
      ...parseSpecialUsableKey(key),
    }))
  )
}

// service.GetUserUsableGroups: the groups a user may set on a token, with the
// description shown for each.
export function resolveUsableGroups(
  rules: GroupRules,
  userGroup: string
): Map<string, string> {
  const groups = new Map(Object.entries(rules.userUsableGroups))
  if (userGroup === '') return groups
  for (const [key, description] of Object.entries(
    rules.specialUsableGroups[userGroup] ?? {}
  )) {
    const rule = parseSpecialUsableKey(key)
    if (rule.action === 'remove') groups.delete(rule.group)
    else groups.set(rule.group, description)
  }
  if (!groups.has(userGroup)) groups.set(userGroup, 'User group')
  return groups
}

function hasGroupRatio(rules: GroupRules, group: string): boolean {
  return Object.hasOwn(rules.groupRatio, group)
}

// service.GetUserAutoGroup: the auto order filtered to groups the user can
// select and that have a ratio, without duplicates.
export function resolveAutoGroups(
  rules: GroupRules,
  userGroup: string
): string[] {
  const usable = resolveUsableGroups(rules, userGroup)
  const groups: string[] = []
  for (const group of rules.autoGroups) {
    if (group === '' || group === AUTO_GROUP) continue
    if (!usable.has(group) || !hasGroupRatio(rules, group)) continue
    if (groups.includes(group)) continue
    groups.push(group)
  }
  return groups
}

export type GroupRatioSource = 'override' | 'group' | 'fallback'

// relay/helper.HandleGroupRatio and service.GetUserGroupRatio: an
// inter-group override for this user group wins, then the billing group's own
// ratio, then 1.
export function resolveGroupRatio(
  rules: GroupRules,
  userGroup: string,
  billingGroup: string
): { ratio: number; source: GroupRatioSource } {
  const overrides = rules.groupGroupRatio[userGroup]
  if (overrides && Object.hasOwn(overrides, billingGroup)) {
    return { ratio: overrides[billingGroup], source: 'override' }
  }
  if (hasGroupRatio(rules, billingGroup)) {
    return { ratio: rules.groupRatio[billingGroup], source: 'group' }
  }
  return { ratio: 1, source: 'fallback' }
}

export type AutoCandidate = {
  group: string
  // undefined when the model's channel groups are unknown.
  hasChannel: boolean | undefined
}

export type GroupSimulation =
  | {
      status: 'rejected'
      reason: 'not_usable' | 'not_priced'
      tokenGroup: string
    }
  | { status: 'no_auto_groups' }
  | { status: 'no_channel'; candidates: AutoCandidate[] }
  | {
      status: 'ok'
      // The group named by the token, or the user's group for a token
      // without one.
      usingGroup: string
      billingGroup: string
      autoCandidates?: AutoCandidate[]
      // false when no enabled channel of the billing group serves the
      // model; undefined when unknown.
      hasChannel: boolean | undefined
      ratio: number
      ratioSource: GroupRatioSource
      groupRatio: number | undefined
    }

// One call through the gateway: middleware.TokenAuth checks the token group,
// service.CacheGetRandomSatisfiedChannel picks the auto group and
// relay/helper.HandleGroupRatio prices it. modelGroups lists the groups with
// an enabled channel for the model.
export function simulateGroupCall(
  rules: GroupRules,
  input: { userGroup: string; tokenGroup: string; modelGroups?: string[] }
): GroupSimulation {
  let usingGroup = input.userGroup
  if (input.tokenGroup !== '') {
    if (!resolveUsableGroups(rules, input.userGroup).has(input.tokenGroup)) {
      return {
        status: 'rejected',
        reason: 'not_usable',
        tokenGroup: input.tokenGroup,
      }
    }
    if (
      input.tokenGroup !== AUTO_GROUP &&
      !hasGroupRatio(rules, input.tokenGroup)
    ) {
      return {
        status: 'rejected',
        reason: 'not_priced',
        tokenGroup: input.tokenGroup,
      }
    }
    usingGroup = input.tokenGroup
  }

  const channelIn = (group: string) =>
    input.modelGroups ? input.modelGroups.includes(group) : undefined

  let billingGroup = usingGroup
  let autoCandidates: AutoCandidate[] | undefined
  if (usingGroup === AUTO_GROUP) {
    autoCandidates = resolveAutoGroups(rules, input.userGroup).map((group) => ({
      group,
      hasChannel: channelIn(group),
    }))
    if (autoCandidates.length === 0) return { status: 'no_auto_groups' }
    const selected = autoCandidates.find(
      (candidate) => candidate.hasChannel !== false
    )
    if (!selected) return { status: 'no_channel', candidates: autoCandidates }
    billingGroup = selected.group
  }

  const resolved = resolveGroupRatio(rules, input.userGroup, billingGroup)
  return {
    status: 'ok',
    usingGroup,
    billingGroup,
    autoCandidates,
    hasChannel: channelIn(billingGroup),
    ratio: resolved.ratio,
    ratioSource: resolved.source,
    groupRatio: hasGroupRatio(rules, billingGroup)
      ? rules.groupRatio[billingGroup]
      : undefined,
  }
}

export function overrideRuleCount(rules: GroupRules) {
  return Object.values(rules.groupGroupRatio).reduce(
    (count, targets) => count + Object.keys(targets).length,
    0
  )
}

// service/payment quoteTopup: the amount × the user group's top-up ratio,
// where a missing or non-positive ratio counts as 1.
export function topupDue(amountMinor: number, ratio: number | undefined) {
  const effective = ratio !== undefined && ratio > 0 ? ratio : 1
  return Math.round(amountMinor * effective)
}
