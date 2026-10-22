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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from '@/components/ui/field'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import {
  getOptionValue,
  useSystemOptions,
} from '@/features/system-settings/hooks/use-system-options'
import { useUpdateOption } from '@/features/system-settings/hooks/use-update-option'
import { ToolPriceSettings } from '@/features/system-settings/models/tool-price-settings'

const PRICING_OPTION_DEFAULTS = {
  ExposeRatioEnabled: false,
  'tool_price_setting.prices': '{}',
}

/** Site-wide pricing settings that belong to no single model. */
export function PricingSettingsDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Pricing settings')}
      description={t('Settings that apply to the prices of every model.')}
      contentClassName='sm:max-w-4xl'
    >
      {props.open && <PricingSettings />}
    </Dialog>
  )
}

function PricingSettings() {
  const { t } = useTranslation()
  const exposeId = useId()
  const options = useSystemOptions()
  const updateOption = useUpdateOption()
  const values = getOptionValue(options.data?.data, PRICING_OPTION_DEFAULTS)

  let content = <LoadingState />
  if (options.isError) {
    content = (
      <ErrorState
        description={options.error.message}
        onRetry={() => void options.refetch()}
      />
    )
  } else if (options.data) {
    content = (
      <div className='space-y-6'>
        <Field orientation='horizontal'>
          <FieldContent>
            <FieldLabel htmlFor={exposeId}>{t('Expose ratio API')}</FieldLabel>
            <FieldDescription id={`${exposeId}-description`}>
              {t('Allow clients to query configured prices via `/api/ratio`.')}
            </FieldDescription>
          </FieldContent>
          <Switch
            id={exposeId}
            aria-describedby={`${exposeId}-description`}
            checked={values.ExposeRatioEnabled}
            disabled={updateOption.isPending}
            onCheckedChange={(checked) =>
              updateOption.mutate({
                key: 'ExposeRatioEnabled',
                value: checked,
              })
            }
          />
        </Field>
        <Separator />
        <section className='space-y-3'>
          <div className='space-y-1'>
            <h3 className='text-sm font-medium'>{t('Tool prices')}</h3>
            <p className='text-muted-foreground text-xs'>
              {t(
                'USD per 1,000 tool calls such as web search, added to the model price.'
              )}
            </p>
          </div>
          <ToolPriceSettings
            defaultValue={values['tool_price_setting.prices']}
          />
        </section>
      </div>
    )
  }

  return content
}
