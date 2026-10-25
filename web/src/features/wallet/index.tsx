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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'

import { AffiliateRewardsCard } from './components/affiliate-rewards-card'
import { BillingHistoryDialog } from './components/dialogs/billing-history-dialog'
import { GatewayQrPaymentDialog } from './components/dialogs/gateway-qr-payment-dialog'
import { PaymentResultDialog } from './components/dialogs/payment-result-dialog'
import { StripePaymentDialog } from './components/dialogs/stripe-payment-dialog'
import { TransferDialog } from './components/dialogs/transfer-dialog'
import { RechargeFormCard } from './components/recharge-form-card'
import { SubscriptionPlansCard } from './components/subscription-plans-card'
import { WalletStatsCard } from './components/wallet-stats-card'
import {
  useAffiliate,
  usePaymentCheckout,
  usePaymentInfo,
  usePaymentMethods,
  useRedemption,
  useRedemptionInfo,
} from './hooks'
import {
  paymentQueryKeys,
  subscriptionQueryKeys,
  walletUserQueryOptions,
} from './lib/wallet-queries'

interface WalletProps {
  initialShowHistory?: boolean
  /** Order whose result is shown, from ?payment_order= */
  paymentOrder?: string
  onPaymentOrderChange: (orderNo: string | undefined) => void
}

export function Wallet(props: WalletProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)
  const [billingDialogOpen, setBillingDialogOpen] = useState(false)
  const [redemptionCode, setRedemptionCode] = useState('')
  const [showSubscriptionPanel, setShowSubscriptionPanel] = useState(true)

  const paymentInfo = usePaymentInfo()
  const topupMethods = usePaymentMethods('topup')
  const checkout = usePaymentCheckout(props.onPaymentOrderChange)
  const {
    affiliateLink,
    loading: affiliateLoading,
    transferQuota,
    transferring,
  } = useAffiliate()
  const { redeeming, redeemCode } = useRedemption()
  const redemptionInfo = useRedemptionInfo()

  const userQuery = useQuery(walletUserQueryOptions)
  const user = userQuery.data ?? null
  const fetchUser = userQuery.refetch

  useEffect(() => {
    if (props.initialShowHistory) {
      setBillingDialogOpen(true)
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [props.initialShowHistory])

  // A fulfilled order changed the balance, the subscriptions or both.
  const handleOrderSettled = useCallback(() => {
    void fetchUser()
    void queryClient.invalidateQueries({ queryKey: paymentQueryKeys.orders })
    void queryClient.invalidateQueries({ queryKey: subscriptionQueryKeys.self })
  }, [fetchUser, queryClient])

  const handleResume = async (orderNo: string) => {
    if (await checkout.resumeOrder(orderNo)) {
      setBillingDialogOpen(false)
    }
  }

  // Handle redemption
  const handleRedeem = async () => {
    if (!redemptionCode) return

    const success = await redeemCode(redemptionCode)
    if (success) {
      setRedemptionCode('')
      await fetchUser()
    }
  }

  // Handle transfer
  const handleTransfer = async (amount: number) => {
    const success = await transferQuota(amount)
    if (success) {
      await fetchUser()
    }
    return success
  }

  const handleSubscriptionAvailabilityChange = useCallback(
    (available: boolean) => {
      setShowSubscriptionPanel(available)
    },
    []
  )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <WalletStatsCard user={user} loading={userQuery.isLoading} />

            <div
              className={
                showSubscriptionPanel
                  ? 'grid gap-4 xl:grid-cols-[minmax(0,1.05fr)_minmax(360px,0.95fr)] xl:items-start'
                  : 'grid gap-4'
              }
            >
              <div id='wallet-add-funds' className='scroll-mt-4'>
                <RechargeFormCard
                  info={paymentInfo.data}
                  loading={paymentInfo.isLoading}
                  methods={topupMethods.data ?? []}
                  methodsLoading={topupMethods.isLoading}
                  onPurchase={checkout.startPurchase}
                  purchasing={checkout.starting}
                  redemptionCode={redemptionCode}
                  onRedemptionCodeChange={setRedemptionCode}
                  onRedeem={handleRedeem}
                  redeeming={redeeming}
                  redemptionEnabled={redemptionInfo.data?.enable_redemption}
                  topupLink={redemptionInfo.data?.topup_link}
                  onOpenBilling={() => setBillingDialogOpen(true)}
                />
              </div>

              <SubscriptionPlansCard
                currency={paymentInfo.data?.currency ?? 'USD'}
                onAvailabilityChange={handleSubscriptionAvailabilityChange}
                userQuota={user?.quota}
                onPurchase={checkout.startPurchase}
                purchasing={checkout.starting}
              />
            </div>

            <AffiliateRewardsCard
              user={user}
              affiliateLink={affiliateLink}
              onTransfer={() => setTransferDialogOpen(true)}
              complianceConfirmed={
                paymentInfo.data?.payment_compliance_confirmed !== false
              }
              loading={affiliateLoading}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <TransferDialog
        open={transferDialogOpen}
        onOpenChange={setTransferDialogOpen}
        onConfirm={handleTransfer}
        availableQuota={user?.aff_quota ?? 0}
        transferring={transferring}
      />

      <BillingHistoryDialog
        open={billingDialogOpen}
        onOpenChange={setBillingDialogOpen}
        onResume={handleResume}
        resuming={checkout.starting}
      />

      <StripePaymentDialog
        session={checkout.stripeSession}
        onClose={checkout.closeStripe}
        onConfirmed={(orderNo) => {
          checkout.closeStripe()
          props.onPaymentOrderChange(orderNo)
        }}
      />

      <GatewayQrPaymentDialog
        session={checkout.qrSession}
        onClose={checkout.closeQr}
        onConfirmed={(orderNo) => {
          checkout.closeQr()
          props.onPaymentOrderChange(orderNo)
        }}
      />

      <PaymentResultDialog
        orderNo={props.paymentOrder}
        onClose={() => props.onPaymentOrderChange(undefined)}
        onSettled={handleOrderSettled}
        onResume={(orderNo) => void checkout.resumeOrder(orderNo)}
        resuming={checkout.starting}
      />
    </>
  )
}
