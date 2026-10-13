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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { formatDateTimeObject } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import {
  getPaymentEvent,
  reviewPaymentEvent,
  type PaymentReviewAction,
} from './payment-event-api'

const reviewSchema = z.object({ reason: z.string().trim().min(1).max(500) })

export function PaymentEventReviewDialog(props: {
  eventId: number
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [action, setAction] = useState<PaymentReviewAction | null>(null)
  const form = useForm({
    resolver: zodResolver(reviewSchema),
    defaultValues: { reason: '' },
    mode: 'onChange',
  })
  const query = useQuery({
    queryKey: ['payment-event', props.eventId],
    queryFn: ({ signal }) => getPaymentEvent(props.eventId, signal),
  })
  const verification = useSecureVerification()
  const cancelVerification = verification.cancel
  const operation = useRef<AbortController | null>(null)
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  useEffect(
    () => () => {
      operation.current?.abort()
      cancelVerification()
    },
    [cancelVerification, userId, sessionId]
  )
  const mutation = useMutation({
    mutationFn: async (choice: PaymentReviewAction) => {
      if (!query.data) return false
      const values = reviewSchema.parse(form.getValues())
      const context = {
        event_id: props.eventId,
        revision: query.data.event.revision,
        action: choice,
        reason: values.reason,
      }
      const controller = new AbortController()
      operation.current = controller
      const proof = await verification.requestVerification({
        scope: 'payment.event.review',
        context,
      })
      if (!proof || controller.signal.aborted) return false
      await reviewPaymentEvent(context, proof.proof_token, controller.signal)
      return true
    },
    onSuccess: (changed) => {
      if (!changed) return
      toast.success(t('Payment event updated'))
      form.reset()
      setAction(null)
    },
    onError: (error) => handleServerError(error),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['payment-events'] })
      void client.invalidateQueries({
        queryKey: ['payment-event', props.eventId],
      })
    },
  })
  const data = query.data
  const disabled =
    !form.formState.isValid ||
    mutation.isPending ||
    query.isFetching ||
    data?.event.status === 'completed'
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!mutation.isPending) props.onOpenChange(open)
        }}
        title={t('Review payment')}
        contentClassName='sm:max-w-3xl'
      >
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {data && (
          <div className='space-y-4'>
            <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-2 [&_dd]:break-all'>
              <div>
                <dt className='text-muted-foreground'>{t('Order number')}</dt>
                <dd>{data.event.order_no}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Transaction ID')}</dt>
                <dd>{data.event.trade_no}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('User ID')}</dt>
                <dd>{formatNumber(data.order.user_id, locale)}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Provider')}</dt>
                <dd>{data.order.provider || '-'}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Payment amount')}</dt>
                <dd>
                  {new Intl.NumberFormat(locale, {
                    style: 'currency',
                    currency: data.event.currency,
                  }).format(Number(data.amount_decimal))}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Status')}</dt>
                <dd>
                  <PaymentEventStatusLabel status={data.event.status} />
                </dd>
              </div>
            </dl>
            <p className='text-sm' role='status'>
              <PaymentFailureReason reason={data.event.last_error} />
            </p>
            {data.order.renewal_stopped && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Renewal grants are stopped. Cancel recurring billing in Stripe to stop further charges.'
                )}
              </p>
            )}
            <div className='space-y-2'>
              <Label htmlFor='payment-review-reason'>
                {t('Review reason')}
              </Label>
              <Textarea
                id='payment-review-reason'
                maxLength={500}
                disabled={
                  mutation.isPending || data.event.status === 'completed'
                }
                {...form.register('reason')}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Record your findings or the external resolution reference. Do not enter credentials.'
                )}
              </p>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                disabled={disabled}
                onClick={() => setAction('retry')}
              >
                {t('Retry verification')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={disabled}
                onClick={() => setAction('hold')}
              >
                {t('Hold for review')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={disabled}
                onClick={() => setAction('resolve')}
              >
                {t('Close review')}
              </Button>
            </div>
            <h3 className='font-medium'>{t('Review history')}</h3>
            <StaticDataTable
              data={data.actions}
              getRowKey={(record) => record.id}
              emptyContent={t('No review history')}
              columns={[
                {
                  id: 'actor',
                  header: t('Operator'),
                  cell: (record) => formatNumber(record.actor_id, locale),
                },
                {
                  id: 'action',
                  header: t('Action'),
                  cell: (record) => (
                    <PaymentReviewActionLabel action={record.action} />
                  ),
                },
                {
                  id: 'reason',
                  header: t('Review reason'),
                  cell: (record) => (
                    <span className='break-words whitespace-pre-wrap'>
                      {record.reason}
                    </span>
                  ),
                },
                {
                  id: 'time',
                  header: t('Time'),
                  cell: (record) =>
                    formatDateTimeObject(new Date(record.created_at * 1000)),
                },
              ]}
            />
          </div>
        )}
      </Dialog>
      <ConfirmDialog
        open={action !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setAction(null)
        }}
        title={<PaymentReviewActionLabel action={action ?? 'retry'} />}
        desc={
          action === 'retry'
            ? t(
                'Verify this payment again. Credit is granted only after payment verification succeeds.'
              )
            : t(
                'This action does not grant credit or issue a refund. Your review reason will be recorded.'
              )
        }
        disabled={disabled}
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (action) mutation.mutate(action)
        }}
      />
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}

export function PaymentEventStatusLabel(props: { status: string }) {
  const { t } = useTranslation()
  switch (props.status) {
    case 'pending':
      return t('Pending')
    case 'review':
      return t('Needs review')
    case 'rejected':
      return t('Rejected')
    case 'completed':
      return t('Completed')
    case 'ignored':
      return t('Ignored')
    case 'resolved':
      return t('Review closed')
    default:
      return props.status
  }
}

function PaymentReviewActionLabel(props: { action: PaymentReviewAction }) {
  const { t } = useTranslation()
  if (props.action === 'retry') return t('Retry verification')
  if (props.action === 'hold') return t('Hold for review')
  return t('Close review')
}

function PaymentFailureReason(props: { reason: string }) {
  const { t } = useTranslation()
  switch (props.reason) {
    case 'subscription_cancelled':
      return t('Renewal grants are stopped for this subscription.')
    case 'billing_period_elapsed':
      return t('The paid billing period has already ended.')
    case 'provider_review':
      return t('The payment provider requires a review.')
    case 'verification_failed':
      return t(
        'Payment verification failed. Check the order and provider transaction.'
      )
    case 'verification_unavailable':
      return t('Payment verification is temporarily unavailable.')
    case 'record_missing':
      return t('A record required for settlement is missing.')
    case 'retry_exhausted':
      return t(
        'Automatic retries are exhausted. Review this payment before retrying.'
      )
    case 'manual_review':
      return t('Automatic processing is paused for review.')
    default:
      return props.reason || null
  }
}
