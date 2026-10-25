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
import { ExternalLink } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'

import { Button } from '@/components/ui/button'

interface PaymentQrCodeProps {
  /** What to scan with: the code's accessible name and its caption */
  label: string
  /** The content drawn as a QR code */
  value?: string
  /** A QR image the payment provider drew, shown instead of value */
  imageUrl?: string
  /** A page or app link that pays on this device instead of scanning */
  openUrl?: string
  openLabel?: string
}

/** A QR code to scan with a payment app, and optionally a link to pay on this device. */
export function PaymentQrCode(props: PaymentQrCodeProps) {
  return (
    <div className='flex flex-col items-center gap-3'>
      <div className='rounded-lg bg-white p-3'>
        {props.imageUrl ? (
          <img
            src={props.imageUrl}
            alt={props.label}
            width={208}
            height={208}
            className='h-52 w-52'
          />
        ) : (
          <QRCodeSVG value={props.value ?? ''} size={208} title={props.label} />
        )}
      </div>
      <p className='text-muted-foreground text-center text-sm'>{props.label}</p>
      {props.openUrl && (
        <Button
          variant='outline'
          className='w-full sm:w-auto'
          render={<a href={props.openUrl} />}
        >
          <ExternalLink data-icon='inline-start' />
          {props.openLabel}
        </Button>
      )}
    </div>
  )
}
