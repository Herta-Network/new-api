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
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import type { QrSession } from '../../hooks/use-payment-checkout'
import { usePaymentOrderResult } from '../../hooks/use-payment-order-result'
import { PAYMENT_ORDER_STATUS } from '../../types'
import { PaymentQrCode } from './payment-qr-code'

interface GatewayQrPaymentDialogProps {
  session: QrSession | null
  onClose: () => void
  /** The order stopped waiting for its payment: the order result takes over */
  onConfirmed: (orderNo: string) => void
}

/**
 * Shows the QR code a gateway answered a checkout with and follows the
 * order until the payment settles it or it closes. A code that is a web
 * address can also be opened on this device.
 */
export function GatewayQrPaymentDialog(props: GatewayQrPaymentDialogProps) {
  const { t } = useTranslation()
  const session = props.session
  const orderNo = session?.orderNo
  const result = usePaymentOrderResult(orderNo)
  const orderStatus = result.order?.status
  const onConfirmed = props.onConfirmed
  useEffect(() => {
    if (
      orderNo &&
      orderStatus !== undefined &&
      orderStatus !== PAYMENT_ORDER_STATUS.PENDING
    ) {
      onConfirmed(orderNo)
    }
  }, [onConfirmed, orderNo, orderStatus])

  const webPage =
    session && /^https?:\/\//i.test(session.qrCode) ? session.qrCode : undefined

  return (
    <Dialog
      open={!!session}
      onOpenChange={(open) => !open && props.onClose()}
      title={t('Scan to pay')}
      description={t(
        'This page updates by itself once the payment provider confirms the payment.'
      )}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-lg'
      contentHeight='auto'
    >
      <div className='space-y-4'>
        {session && (
          <PaymentQrCode
            value={session.qrCode}
            label={t('Scan the QR code with your payment app')}
            openUrl={webPage}
            openLabel={t('Open the payment page on this device')}
          />
        )}
        <div className='flex justify-end'>
          <Button type='button' variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
