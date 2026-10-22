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
import { CheckinSettingsSection } from '../general/checkin-settings-section'
import { QuotaSettingsSection } from '../general/quota-settings-section'
import { CurrencySettingsSection } from '../integrations/currency-settings-section'
import { PaymentMethodsSection } from '../integrations/payment-methods-section'
import { PaymentSettingsSection } from '../integrations/payment-settings-section'
import { RatioSettingsCard } from '../models/ratio-settings-card'
import type { BillingSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'

const getGroupDefaults = (settings: BillingSettings) => ({
  TopupGroupRatio: settings.TopupGroupRatio,
  GroupRatio: settings.GroupRatio,
  UserUsableGroups: settings.UserUsableGroups,
  GroupGroupRatio: settings.GroupGroupRatio,
  AutoGroups: settings.AutoGroups,
  MaxTokenAutoGroups: settings.MaxTokenAutoGroups,
  DefaultUseAutoGroup: settings.DefaultUseAutoGroup,
  GroupSpecialUsableGroup:
    settings['group_ratio_setting.group_special_usable_group'],
})

const BILLING_SECTIONS = [
  {
    id: 'quota',
    titleKey: 'Quota Settings',
    build: (settings: BillingSettings) => (
      <QuotaSettingsSection
        defaultValues={{
          QuotaForNewUser: settings.QuotaForNewUser,
          QuotaForInviter: settings.QuotaForInviter,
          QuotaForInvitee: settings.QuotaForInvitee,
          TopUpLink: settings.TopUpLink,
          quota_setting: {
            trust_quota_usd: settings['quota_setting.trust_quota_usd'],
            pre_consume_multiplier:
              settings['quota_setting.pre_consume_multiplier'],
            enable_free_model_pre_consume:
              settings['quota_setting.enable_free_model_pre_consume'],
          },
        }}
        complianceConfirmed={
          (settings['payment_setting.compliance_confirmed'] ?? false) &&
          settings['payment_setting.compliance_terms_version'] === 'v1'
        }
      />
    ),
  },
  {
    id: 'group-pricing',
    titleKey: 'Group Pricing',
    build: (settings: BillingSettings) => (
      <RatioSettingsCard
        titleKey='Group Pricing'
        groupDefaults={getGroupDefaults(settings)}
      />
    ),
  },
  {
    id: 'currency',
    titleKey: 'Currency & Exchange Rates',
    build: (settings: BillingSettings) => (
      <CurrencySettingsSection
        defaultValues={{
          currency: settings['payment_setting.currency'],
          quotaDisplayMode: settings['general_setting.quota_display_mode'],
          overrides: settings['payment_setting.exchange_rate_overrides'],
          billingApiUsesTokenQuota:
            settings['general_setting.billing_api_uses_token_quota'],
          exchangeRateKeyConfigured: settings.PaymentExchangeRateKeyConfigured,
          quotaPerUnit: settings.QuotaPerUnit,
        }}
      />
    ),
  },
  {
    id: 'payment',
    titleKey: 'Payment Settings',
    build: (settings: BillingSettings) => (
      <PaymentSettingsSection
        defaultValues={{
          currency: settings['payment_setting.currency'],
          minTopup: settings['payment_setting.min_topup'],
          amountOptions: settings['payment_setting.amount_options'],
          amountDiscount: settings['payment_setting.amount_discount'],
        }}
        complianceDefaults={{
          confirmed: settings['payment_setting.compliance_confirmed'] ?? false,
          termsVersion:
            settings['payment_setting.compliance_terms_version'] ?? '',
          confirmedAt: settings['payment_setting.compliance_confirmed_at'] ?? 0,
          confirmedBy: settings['payment_setting.compliance_confirmed_by'] ?? 0,
        }}
      />
    ),
  },
  {
    id: 'payment-methods',
    titleKey: 'Payment Methods',
    build: (settings: BillingSettings) => (
      <PaymentMethodsSection currency={settings['payment_setting.currency']} />
    ),
  },
  {
    id: 'checkin',
    titleKey: 'Check-in Rewards',
    build: (settings: BillingSettings) => (
      <CheckinSettingsSection
        defaultValues={{
          enabled: settings['checkin_setting.enabled'],
          minQuota: settings['checkin_setting.min_quota'],
          maxQuota: settings['checkin_setting.max_quota'],
        }}
      />
    ),
  },
] as const

export type BillingSectionId = (typeof BILLING_SECTIONS)[number]['id']

const billingRegistry = createSectionRegistry<
  BillingSectionId,
  BillingSettings
>({
  sections: BILLING_SECTIONS,
  defaultSection: 'quota',
  basePath: '/admin/system-settings/billing',
  urlStyle: 'path',
})

export const BILLING_SECTION_IDS = billingRegistry.sectionIds
export const BILLING_DEFAULT_SECTION = billingRegistry.defaultSection
export const getBillingSectionNavItems = billingRegistry.getSectionNavItems
export const getBillingSectionContent = billingRegistry.getSectionContent
export const getBillingSectionMeta = billingRegistry.getSectionMeta
