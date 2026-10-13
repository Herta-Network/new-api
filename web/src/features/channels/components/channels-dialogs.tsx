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
import { lazy } from 'react'

import { DeferredDialog } from '@/components/deferred-dialog'

import { CHANNEL_TYPE_VLLM, CHANNEL_TYPE_SGLANG } from '../constants'
import { useChannels } from './channels-provider'

const BalanceQueryDialog = lazy(() =>
  import('./dialogs/balance-query-dialog').then((module) => ({
    default: module.BalanceQueryDialog,
  }))
)
const ChannelTestDialog = lazy(() =>
  import('./dialogs/channel-test-dialog').then((module) => ({
    default: module.ChannelTestDialog,
  }))
)
const CopyChannelDialog = lazy(() =>
  import('./dialogs/copy-channel-dialog').then((module) => ({
    default: module.CopyChannelDialog,
  }))
)
const EditTagDialog = lazy(() =>
  import('./dialogs/edit-tag-dialog').then((module) => ({
    default: module.EditTagDialog,
  }))
)
const FetchModelsDialog = lazy(() =>
  import('./dialogs/fetch-models-dialog').then((module) => ({
    default: module.FetchModelsDialog,
  }))
)
const InferenceStatusDialog = lazy(() =>
  import('./dialogs/inference-status-dialog').then((module) => ({
    default: module.InferenceStatusDialog,
  }))
)
const MultiKeyManageDialog = lazy(() =>
  import('./dialogs/multi-key-manage-dialog').then((module) => ({
    default: module.MultiKeyManageDialog,
  }))
)
const OllamaModelsDialog = lazy(() =>
  import('./dialogs/ollama-models-dialog').then((module) => ({
    default: module.OllamaModelsDialog,
  }))
)
const TagBatchEditDialog = lazy(() =>
  import('./dialogs/tag-batch-edit-dialog').then((module) => ({
    default: module.TagBatchEditDialog,
  }))
)
const UpstreamUpdateDialog = lazy(() =>
  import('./dialogs/upstream-update-dialog').then((module) => ({
    default: module.UpstreamUpdateDialog,
  }))
)
const ChannelMutateDrawer = lazy(() =>
  import('./drawers/channel-mutate-drawer').then((module) => ({
    default: module.ChannelMutateDrawer,
  }))
)

export function ChannelsDialogs() {
  const { open, setOpen, currentRow, upstream } = useChannels()

  return (
    <>
      {open === 'inference-status' &&
        currentRow &&
        [CHANNEL_TYPE_VLLM, CHANNEL_TYPE_SGLANG].includes(currentRow.type) && (
          <DeferredDialog
            open={open === 'inference-status'}
            onOpenChange={(next) => !next && setOpen(null)}
          >
            <InferenceStatusDialog
              key={currentRow.id}
              provider={
                currentRow.type === CHANNEL_TYPE_SGLANG ? 'sglang' : 'vllm'
              }
              channelId={currentRow.id}
              channelName={currentRow.name}
              onClose={() => setOpen(null)}
              onSyncModels={() => setOpen('fetch-models')}
              onTestChannel={() => setOpen('test-channel')}
            />
          </DeferredDialog>
        )}
      {/* Channel Create/Update Drawer */}
      <DeferredDialog
        open={open === 'create-channel' || open === 'update-channel'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <ChannelMutateDrawer
          open={open === 'create-channel' || open === 'update-channel'}
          onOpenChange={(v) => !v && setOpen(null)}
          currentRow={open === 'update-channel' ? currentRow : null}
        />
      </DeferredDialog>

      {/* Test Channel Dialog */}
      <DeferredDialog
        open={open === 'test-channel'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <ChannelTestDialog
          open={open === 'test-channel'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Balance Query Dialog */}
      <DeferredDialog
        open={open === 'balance-query'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <BalanceQueryDialog
          open={open === 'balance-query'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Fetch Models Dialog */}
      <DeferredDialog
        open={open === 'fetch-models'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <FetchModelsDialog
          open={open === 'fetch-models'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Ollama Models Dialog */}
      <DeferredDialog
        open={open === 'ollama-models'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <OllamaModelsDialog
          open={open === 'ollama-models'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Copy Channel Dialog */}
      <DeferredDialog
        open={open === 'copy-channel'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <CopyChannelDialog
          open={open === 'copy-channel'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Multi-Key Management Dialog */}
      <DeferredDialog
        open={open === 'multi-key-manage'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <MultiKeyManageDialog
          open={open === 'multi-key-manage'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Tag Batch Edit Dialog */}
      <DeferredDialog
        open={open === 'tag-batch-edit'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <TagBatchEditDialog
          open={open === 'tag-batch-edit'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Edit Tag Dialog */}
      <DeferredDialog
        open={open === 'edit-tag'}
        onOpenChange={(next) => !next && setOpen(null)}
      >
        <EditTagDialog
          open={open === 'edit-tag'}
          onOpenChange={(v) => !v && setOpen(null)}
        />
      </DeferredDialog>

      {/* Upstream Model Update Dialog */}
      <DeferredDialog
        open={upstream.showModal}
        onOpenChange={(next) => !next && upstream.closeModal()}
      >
        <UpstreamUpdateDialog upstream={upstream} />
      </DeferredDialog>
    </>
  )
}
