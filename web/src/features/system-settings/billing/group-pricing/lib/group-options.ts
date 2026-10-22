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
import { z } from 'zod'

import {
  formatJsonForTextarea,
  normalizeJsonString,
  validateJsonString,
  type JsonValidationError,
} from '../../../models/utils'
import { positiveIntegerSchema } from '../../../utils/numeric-field'
import {
  parseDescriptionMap,
  parseGroupList,
  parseNestedRatioMap,
  parseRatioMap,
  parseSpecialUsableMap,
  type DescriptionMap,
  type GroupRules,
  type NestedRatioMap,
  type ParsedOption,
  type RatioMap,
  type SpecialUsableMap,
} from './group-rules'

export type GroupPricingValues = {
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  MaxTokenAutoGroups: number
  DefaultUseAutoGroup: boolean
  GroupSpecialUsableGroup: string
  TopupGroupRatio: string
}

export type GroupPricingField = keyof GroupPricingValues

export type GroupJsonField = Exclude<
  GroupPricingField,
  'MaxTokenAutoGroups' | 'DefaultUseAutoGroup'
>

// Every field is saved under its own option key except the special usable
// rules, which live in the group_ratio_setting config.
export const GROUP_OPTION_KEYS: Record<GroupPricingField, string> = {
  GroupRatio: 'GroupRatio',
  UserUsableGroups: 'UserUsableGroups',
  GroupGroupRatio: 'GroupGroupRatio',
  AutoGroups: 'AutoGroups',
  MaxTokenAutoGroups: 'MaxTokenAutoGroups',
  DefaultUseAutoGroup: 'DefaultUseAutoGroup',
  GroupSpecialUsableGroup: 'group_ratio_setting.group_special_usable_group',
  TopupGroupRatio: 'TopupGroupRatio',
}

const GROUP_FIELDS = Object.keys(GROUP_OPTION_KEYS) as GroupPricingField[]

const JSON_FIELD_PARSERS: Record<
  GroupJsonField,
  (text: string) => ParsedOption<unknown>
> = {
  GroupRatio: (text) => {
    const parsed = parseRatioMap(text)
    if (parsed.ok && Object.values(parsed.value).some((ratio) => ratio < 0)) {
      return { ok: false }
    }
    return parsed
  },
  UserUsableGroups: parseDescriptionMap,
  GroupGroupRatio: parseNestedRatioMap,
  AutoGroups: parseGroupList,
  GroupSpecialUsableGroup: parseSpecialUsableMap,
  TopupGroupRatio: parseRatioMap,
}

export const JSON_FIELD_SHAPES: Record<GroupJsonField, string> = {
  GroupRatio: 'Expected a JSON object of group → ratio (0 or more)',
  UserUsableGroups: 'Expected a JSON object of group → description',
  GroupGroupRatio:
    'Expected a JSON object of user group → { billing group → ratio }',
  AutoGroups: 'Expected a JSON array of group identifiers',
  GroupSpecialUsableGroup:
    'Expected a JSON object of user group → { group → description }',
  TopupGroupRatio: 'Expected a JSON object of group → ratio',
}

export function isGroupFieldValid(field: GroupJsonField, text: string) {
  return JSON_FIELD_PARSERS[field](text).ok
}

type Translate = (key: string, options?: Record<string, unknown>) => string

function jsonFieldSchema(t: Translate, field: GroupJsonField) {
  return z.string().superRefine((value, ctx) => {
    if (isGroupFieldValid(field, value)) return
    const syntax = validateJsonString(value)
    const error: JsonValidationError | undefined = syntax.error
    let message = t(JSON_FIELD_SHAPES[field])
    if (!syntax.valid && error?.line && error.column) {
      message = t('JSON is invalid at line {{line}}, column {{column}}.', {
        line: error.line,
        column: error.column,
      })
    } else if (!syntax.valid) {
      message = t('JSON is invalid. Please check the syntax.')
    }
    ctx.addIssue({ code: 'custom', message })
  })
}

export function createGroupPricingSchema(t: Translate) {
  return z.object({
    GroupRatio: jsonFieldSchema(t, 'GroupRatio'),
    UserUsableGroups: jsonFieldSchema(t, 'UserUsableGroups'),
    GroupGroupRatio: jsonFieldSchema(t, 'GroupGroupRatio'),
    AutoGroups: jsonFieldSchema(t, 'AutoGroups'),
    MaxTokenAutoGroups: positiveIntegerSchema(t('Enter a positive integer')),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: jsonFieldSchema(t, 'GroupSpecialUsableGroup'),
    TopupGroupRatio: jsonFieldSchema(t, 'TopupGroupRatio'),
  })
}

function isJsonField(field: GroupPricingField): field is GroupJsonField {
  return field !== 'MaxTokenAutoGroups' && field !== 'DefaultUseAutoGroup'
}

// The form edits pretty-printed JSON; text that does not parse is kept as is.
export function toFormValues(values: GroupPricingValues): GroupPricingValues {
  const result = { ...values }
  for (const field of GROUP_FIELDS) {
    if (isJsonField(field)) result[field] = formatJsonForTextarea(values[field])
  }
  return result
}

function canonicalJson(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalJson)
  if (typeof value !== 'object' || value === null) return value
  return Object.fromEntries(
    Object.keys(value)
      .sort()
      .map((key) => [
        key,
        canonicalJson((value as Record<string, unknown>)[key]),
      ])
  )
}

// Two option values that parse to the same JSON (object key order aside)
// mean the same settings.
function sameOptionValue(a: string, b: string) {
  if (a === b) return true
  try {
    return (
      JSON.stringify(canonicalJson(JSON.parse(a))) ===
      JSON.stringify(canonicalJson(JSON.parse(b)))
    )
  } catch {
    return a.trim() === b.trim()
  }
}

export type GroupOptionUpdate = {
  field: GroupPricingField
  key: string
  value: string | number | boolean
}

// The option writes a save makes: only fields whose meaning changed against
// what the server holds. Options that were not changed are not sent, so the
// stored text stays byte for byte as it was.
export function groupOptionUpdates(
  saved: GroupPricingValues,
  values: GroupPricingValues
): GroupOptionUpdate[] {
  const updates: GroupOptionUpdate[] = []
  for (const field of GROUP_FIELDS) {
    const key = GROUP_OPTION_KEYS[field]
    if (!isJsonField(field)) {
      if (values[field] !== saved[field]) {
        updates.push({ field, key, value: values[field] })
      }
      continue
    }
    if (sameOptionValue(saved[field], values[field])) continue
    updates.push({ field, key, value: normalizeJsonString(values[field]) })
  }
  return updates
}

export type ParsedGroupOptions = {
  rules: GroupRules
  topupGroupRatio: RatioMap
  invalid: Set<GroupJsonField>
}

// Parses every option; one that does not parse is reported in `invalid` and
// read as empty, so callers can refuse to edit it instead of overwriting it.
export function parseGroupOptions(
  values: Pick<GroupPricingValues, GroupJsonField>
): ParsedGroupOptions {
  const invalid = new Set<GroupJsonField>()
  const read = <T>(field: GroupJsonField, empty: T): T => {
    const parsed = JSON_FIELD_PARSERS[field](values[field])
    if (parsed.ok) return parsed.value as T
    invalid.add(field)
    return empty
  }
  return {
    rules: {
      groupRatio: read<RatioMap>('GroupRatio', {}),
      userUsableGroups: read<DescriptionMap>('UserUsableGroups', {}),
      groupGroupRatio: read<NestedRatioMap>('GroupGroupRatio', {}),
      specialUsableGroups: read<SpecialUsableMap>(
        'GroupSpecialUsableGroup',
        {}
      ),
      autoGroups: read<string[]>('AutoGroups', []),
    },
    topupGroupRatio: read<RatioMap>('TopupGroupRatio', {}),
    invalid,
  }
}

export function stringifyOption(value: unknown) {
  return JSON.stringify(value, null, 2)
}

export type GroupRow = {
  name: string
  // undefined when the group has no entry in GroupRatio.
  ratio: number | undefined
  selectable: boolean
  description: string
}

// Every name in GroupRatio or UserUsableGroups, in that order.
export function buildGroupRows(rules: GroupRules): GroupRow[] {
  const names = [
    ...Object.keys(rules.groupRatio),
    ...Object.keys(rules.userUsableGroups).filter(
      (name) => !Object.hasOwn(rules.groupRatio, name)
    ),
  ]
  return names.map((name) => ({
    name,
    ratio: Object.hasOwn(rules.groupRatio, name)
      ? rules.groupRatio[name]
      : undefined,
    selectable: Object.hasOwn(rules.userUsableGroups, name),
    description: rules.userUsableGroups[name] ?? '',
  }))
}

export type GroupDraft = {
  name: string
  // undefined keeps the group out of GroupRatio, as the auto group is.
  ratio: number | undefined
  selectable: boolean
  description: string
}

function withEntry<T>(
  map: Record<string, T>,
  name: string,
  value: T | undefined
): Record<string, T> {
  const next = { ...map }
  if (value === undefined) {
    delete next[name]
    return next
  }
  Object.defineProperty(next, name, {
    value,
    enumerable: true,
    writable: true,
    configurable: true,
  })
  return next
}

// Writes one group's ratio and visibility. Other groups keep their entries
// and their order.
export function writeGroup(rules: GroupRules, draft: GroupDraft) {
  return {
    GroupRatio: stringifyOption(
      withEntry(rules.groupRatio, draft.name, draft.ratio)
    ),
    UserUsableGroups: stringifyOption(
      withEntry(
        rules.userUsableGroups,
        draft.name,
        draft.selectable ? draft.description : undefined
      )
    ),
  }
}

// Removes a group from the list. Rules elsewhere that name it are kept; the
// advanced sections flag them.
export function removeGroup(rules: GroupRules, name: string) {
  return writeGroup(rules, {
    name,
    ratio: undefined,
    selectable: false,
    description: '',
  })
}

export type GroupAccess = 'selectable' | 'assigned' | 'hidden'

// Who can put a token on the group: everyone when it is listed in
// UserUsableGroups, otherwise its members and the user groups a special rule
// grants it to. Without members or grants nobody can use it.
export function groupAccess(
  row: GroupRow,
  grantedTo: string[],
  members: number | undefined
): GroupAccess {
  if (row.selectable) return 'selectable'
  if (grantedTo.length > 0 || members === undefined || members > 0) {
    return 'assigned'
  }
  return 'hidden'
}

// The user group the worked examples are told from: default when it exists.
export function sampleUserGroup(rules: GroupRules): string | undefined {
  const names = Object.keys(rules.groupRatio)
  return names.includes('default') ? 'default' : names[0]
}

export type AdvancedSection =
  | 'overrides'
  | 'special'
  | 'auto'
  | 'max-groups'
  | 'default-auto'
  | 'topup'

// The advanced section that edits each field, opened when the field is
// invalid on save.
export const FIELD_SECTIONS: Partial<
  Record<GroupPricingField, AdvancedSection>
> = {
  GroupGroupRatio: 'overrides',
  GroupSpecialUsableGroup: 'special',
  AutoGroups: 'auto',
  MaxTokenAutoGroups: 'max-groups',
  DefaultUseAutoGroup: 'default-auto',
  TopupGroupRatio: 'topup',
}
