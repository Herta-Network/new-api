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
import { useEffect, useState } from 'react'
import { GenIcon, type IconBaseProps, type IconType } from 'react-icons'

import {
  ICON_PACK_CANDIDATES,
  iconBucket,
  expandIconTree,
  type CompactIconTree,
  type IconPackId,
} from '@/lib/react-icon-data'

import bucketStarts from '../../.generated/react-icons/buckets.json'

type IconBucket = Record<string, CompactIconTree>
const ICON_BUCKET_CACHE = new Map<string, Promise<IconBucket>>()
const ICON_CACHE = new Map<string, IconType>()

function normalizeIconName(name: string | null | undefined): string | null {
  const trimmed = name?.trim()
  if (!trimmed || !/^[A-Z][A-Za-z0-9]*$/.test(trimmed)) return null
  return trimmed
}

function getCandidatePacks(iconName: string): IconPackId[] {
  return (
    ICON_PACK_CANDIDATES.find(([pattern]) => pattern.test(iconName))?.[1] ?? []
  )
}

function loadIconBucket(pack: IconPackId, name: string): Promise<IconBucket> {
  const key = `${pack}-${iconBucket(name, bucketStarts[pack])}`
  const cached = ICON_BUCKET_CACHE.get(key)
  if (cached) return cached
  const promise = import(
    /* webpackChunkName: "react-icon-[request]" */ `../../.generated/react-icons/${key}.json`
  )
    .then((module) => module.default as IconBucket)
    .catch((error: unknown) => {
      ICON_BUCKET_CACHE.delete(key)
      throw error
    })
  ICON_BUCKET_CACHE.set(key, promise)
  return promise
}

async function resolveReactIcon(iconName: string): Promise<IconType | null> {
  const cached = ICON_CACHE.get(iconName)
  if (cached) return cached
  for (const packId of getCandidatePacks(iconName)) {
    try {
      const bucket = await loadIconBucket(packId, iconName)
      if (Object.hasOwn(bucket, iconName)) {
        const Icon = GenIcon(expandIconTree(bucket[iconName]))
        ICON_CACHE.set(iconName, Icon)
        return Icon
      }
    } catch {
      /* An unavailable chunk keeps the existing empty fallback. */
    }
  }
  return null
}

type ReactIconByNameProps = IconBaseProps & {
  name?: string | null
}

type ResolvedIconState = {
  iconName: string
  Icon: IconType | null
}

export function ReactIconByName({ name, ...props }: ReactIconByNameProps) {
  const iconName = normalizeIconName(name)
  const [resolvedIcon, setResolvedIcon] = useState<ResolvedIconState | null>(
    null
  )

  useEffect(() => {
    let cancelled = false

    if (!iconName) return

    void resolveReactIcon(iconName).then((Icon) => {
      if (!cancelled) setResolvedIcon({ iconName, Icon })
    })

    return () => {
      cancelled = true
    }
  }, [iconName])

  if (!iconName || resolvedIcon?.iconName !== iconName || !resolvedIcon.Icon) {
    return null
  }

  const Icon = resolvedIcon.Icon

  return <Icon {...props} />
}
