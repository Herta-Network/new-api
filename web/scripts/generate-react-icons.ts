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
  readFileSync,
  mkdirSync,
  writeFileSync,
  readdirSync,
  unlinkSync,
} from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { IconsManifest, type IconTree } from 'react-icons'

import { ICON_PACK_IDS, type CompactIconTree } from '../src/lib/react-icon-data'

function compactIconTree(node: IconTree): CompactIconTree {
  return node.child.length
    ? [node.tag, node.attr, node.child.map(compactIconTree)]
    : [node.tag, node.attr]
}

function writeGeneratedFile(file: string, contents: string) {
  try {
    if (readFileSync(file, 'utf8') === contents) return
  } catch {
    /* First generation. */
  }
  writeFileSync(file, contents)
}

// Keep the full installed catalog, but avoid downloading a whole icon pack to
// display one name. Generated data is shared by production, dev and Vitest.
export function generateReactIcons(root: string) {
  const require = createRequire(path.join(root, 'package.json'))
  const packageRoot = path.dirname(
    path.dirname(require.resolve('react-icons/ai'))
  )
  const output = path.join(root, '.generated/react-icons')
  mkdirSync(output, { recursive: true })
  const starts: Record<string, string[]> = {}
  const expectedFiles = new Set(['buckets.json', 'LICENSE.txt'])
  for (const pack of ICON_PACK_IDS) {
    const source = readFileSync(
      path.join(packageRoot, pack, 'index.mjs'),
      'utf8'
    )
    const entries: [string, CompactIconTree][] = []
    const declarations = [
      ...source.matchAll(/export function (\w+) \(props\)/g),
    ]
    for (const match of source.matchAll(
      /export function (\w+) \(props\) \{\s*return GenIcon\(([^\n]+)\)\(props\);\s*\};/g
    )) {
      entries.push([
        match[1],
        compactIconTree(JSON.parse(match[2]) as IconTree),
      ])
    }
    if (
      !entries.length ||
      entries.length !== declarations.length ||
      new Set(entries.map(([name]) => name)).size !== entries.length
    ) {
      throw new Error(
        `Unsupported react-icons source format in ${pack}; icon generation must preserve every export`
      )
    }
    // Keep related variants adjacent for compression, with bounded downloads.
    entries.sort(([a], [b]) => {
      if (a === b) return 0
      return a < b ? -1 : 1
    })
    const buckets: Record<string, CompactIconTree>[] = []
    let bytes = 2
    let bucket: Record<string, CompactIconTree> = {}
    const packStarts: string[] = []
    for (const [name, tree] of entries) {
      const entryBytes = Buffer.byteLength(JSON.stringify({ [name]: tree }))
      if (bytes + entryBytes > 128 * 1024 && Object.keys(bucket).length) {
        buckets.push(bucket)
        bucket = {}
        bytes = 2
      }
      if (bytes === 2) packStarts.push(name)
      bucket[name] = tree
      bytes += entryBytes
    }
    buckets.push(bucket)
    starts[pack] = packStarts
    for (let i = 0; i < buckets.length; i++) {
      const name = `${pack}-${i}.json`
      expectedFiles.add(name)
      writeGeneratedFile(path.join(output, name), JSON.stringify(buckets[i]))
    }
  }
  writeGeneratedFile(path.join(output, 'buckets.json'), JSON.stringify(starts))
  const attribution = IconsManifest.filter((pack) =>
    ICON_PACK_IDS.some((id) => id === pack.id)
  )
    .map(
      (pack) =>
        `${pack.name}\n${pack.projectUrl}\n${pack.license}\n${pack.licenseUrl}`
    )
    .join('\n\n')
  writeGeneratedFile(
    path.join(output, 'LICENSE.txt'),
    `${readFileSync(path.join(packageRoot, 'LICENSE'), 'utf8')}\n\nIncluded icon collections:\n\n${attribution}\n`
  )
  for (const name of readdirSync(output)) {
    if (!expectedFiles.has(name) && /^[a-z0-9]+-\d+\.json$/.test(name)) {
      unlinkSync(path.join(output, name))
    }
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  generateReactIcons(
    path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
  )
}
