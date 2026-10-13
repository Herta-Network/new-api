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
import i18next from 'i18next'
import { useCallback, useRef, useState } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { calculateAmount, isApiSuccess, requestPayment } from '../api'
import { submitPaymentForm } from '../lib/payment'

export async function requestPaymentAmount(
  topupAmount: number,
  paymentType: string
) {
  if (!paymentType) return null
  const response = await calculateAmount({
    amount: Math.floor(topupAmount),
    payment_method: paymentType,
  })
  if (!isApiSuccess(response) || !response.data) throw response
  return response.data
}

export function usePayment() {
  const [amount, setAmount] = useState(0)
  const [currency, setCurrency] = useState('')
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)
  const latestQuote = useRef(0)
  const calculatePaymentAmount = useCallback(
    async (topupAmount: number, paymentType: string) => {
      const sequence = ++latestQuote.current
      setCalculating(true)
      try {
        const quote = await requestPaymentAmount(topupAmount, paymentType)
        if (sequence !== latestQuote.current) return false
        setAmount(Number(quote?.amount ?? 0))
        setCurrency(quote?.currency ?? '')
        return quote !== null
      } catch (error) {
        if (sequence === latestQuote.current) {
          setAmount(0)
          setCurrency('')
          handleServerError(error, i18next.t('Payment request failed'))
        }
        return false
      } finally {
        if (sequence === latestQuote.current) setCalculating(false)
      }
    },
    []
  )
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string) => {
      setProcessing(true)
      try {
        const response = await requestPayment({
          amount: Math.floor(topupAmount),
          payment_method: paymentType,
        })
        if (!isApiSuccess(response) || !response.url) {
          handleServerError(response, i18next.t('Payment request failed'))
          return false
        }
        if (response.type === 'form' && response.data) {
          submitPaymentForm(response.url, response.data)
        } else window.location.assign(response.url)
        toast.success(i18next.t('Redirecting to payment page...'))
        return true
      } catch (error) {
        handleServerError(error, i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )
  return {
    amount,
    currency,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    setAmount,
  }
}
