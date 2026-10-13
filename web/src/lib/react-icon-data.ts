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
import type { IconTree } from 'react-icons'

export const ICON_PACK_IDS = [
  'ai',
  'bi',
  'bs',
  'cg',
  'ci',
  'di',
  'fa',
  'fa6',
  'fc',
  'fi',
  'gi',
  'go',
  'gr',
  'hi',
  'hi2',
  'im',
  'io',
  'io5',
  'lia',
  'lu',
  'md',
  'pi',
  'ri',
  'rx',
  'si',
  'sl',
  'tb',
  'tfi',
  'ti',
  'vsc',
  'wi',
] as const
export type IconPackId = (typeof ICON_PACK_IDS)[number]
export type CompactIconTree = [
  string,
  Record<string, string>,
  CompactIconTree[]?,
]

export const ICON_PACK_CANDIDATES: Array<[RegExp, IconPackId[]]> = [
  [/^Ai[A-Z0-9]/, ['ai']],
  [/^Bi[A-Z0-9]/, ['bi']],
  [/^Bs[A-Z0-9]/, ['bs']],
  [/^Cg[A-Z0-9]/, ['cg']],
  [/^Ci[A-Z0-9]/, ['ci']],
  [/^Di[A-Z0-9]/, ['di']],
  [/^Fa[A-Z0-9]/, ['fa6', 'fa']],
  [/^Fc[A-Z0-9]/, ['fc']],
  [/^Fi[A-Z0-9]/, ['fi']],
  [/^Gi[A-Z0-9]/, ['gi']],
  [/^Go[A-Z0-9]/, ['go']],
  [/^Gr[A-Z0-9]/, ['gr']],
  [/^Hi[A-Z0-9]/, ['hi2', 'hi']],
  [/^Im[A-Z0-9]/, ['im']],
  [/^Io[A-Z0-9]/, ['io5', 'io']],
  [/^Lia[A-Z0-9]/, ['lia']],
  [/^Lu[A-Z0-9]/, ['lu']],
  [/^Md[A-Z0-9]/, ['md']],
  [/^Pi[A-Z0-9]/, ['pi']],
  [/^Ri[A-Z0-9]/, ['ri']],
  [/^Rx[A-Z0-9]/, ['rx']],
  [/^Si[A-Z0-9]/, ['si']],
  [/^Sl[A-Z0-9]/, ['sl']],
  [/^Tb[A-Z0-9]/, ['tb']],
  [/^Tfi[A-Z0-9]/, ['tfi']],
  [/^Ti[A-Z0-9]/, ['ti']],
  [/^Vsc[A-Z0-9]/, ['vsc']],
  [/^Wi[A-Z0-9]/, ['wi']],
]

// Names and bucket starts use ordinal order in both the generator and runtime.
export function iconBucket(name: string, starts: readonly string[]): number {
  let low = 0
  let high = starts.length
  while (low < high) {
    const middle = (low + high) >>> 1
    if (starts[middle] <= name) low = middle + 1
    else high = middle
  }
  return Math.max(0, low - 1)
}

export function expandIconTree(node: CompactIconTree): IconTree {
  return {
    tag: node[0],
    attr: node[1],
    child: node[2]?.map(expandIconTree) ?? [],
  }
}
