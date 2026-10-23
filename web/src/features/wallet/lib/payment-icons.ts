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
import type { IconType } from 'react-icons'
import {
  FaBuildingColumns,
  FaCcMastercard,
  FaCcStripe,
  FaCcVisa,
  FaCoins,
  FaCreditCard,
  FaMoneyBillWave,
  FaQrcode,
  FaWallet,
} from 'react-icons/fa6'
import { RiWechatPayFill } from 'react-icons/ri'
import {
  SiAlipay,
  SiApplepay,
  SiBitcoin,
  SiEthereum,
  SiGooglepay,
  SiPaypal,
  SiStripe,
  SiTether,
  SiWechat,
} from 'react-icons/si'

export type PaymentIcon = {
  /** The react-icons component name stored as the payment method's icon. */
  name: string
  /** i18n key of the label administrators pick the icon by. */
  labelKey: string
  Icon: IconType
}

/**
 * The icons an administrator can give a payment method. Each is imported by
 * name so only these icons reach the bundle, and each keeps the react-icons
 * component name administrators typed before the list existed, so methods
 * saved with those names keep their icon. Font Awesome icons come from fa6,
 * which the name lookup used to try before fa.
 */
export const PAYMENT_ICONS: readonly PaymentIcon[] = [
  { name: 'FaCreditCard', labelKey: 'Card', Icon: FaCreditCard },
  { name: 'FaCcVisa', labelKey: 'Visa', Icon: FaCcVisa },
  { name: 'FaCcMastercard', labelKey: 'Mastercard', Icon: FaCcMastercard },
  { name: 'FaBuildingColumns', labelKey: 'Bank', Icon: FaBuildingColumns },
  { name: 'SiAlipay', labelKey: 'Alipay', Icon: SiAlipay },
  { name: 'RiWechatPayFill', labelKey: 'WeChat Pay', Icon: RiWechatPayFill },
  { name: 'SiWechat', labelKey: 'WeChat', Icon: SiWechat },
  { name: 'SiStripe', labelKey: 'Stripe', Icon: SiStripe },
  { name: 'FaCcStripe', labelKey: 'Stripe card', Icon: FaCcStripe },
  { name: 'SiPaypal', labelKey: 'PayPal', Icon: SiPaypal },
  { name: 'SiApplepay', labelKey: 'Apple Pay', Icon: SiApplepay },
  { name: 'SiGooglepay', labelKey: 'Google Pay', Icon: SiGooglepay },
  { name: 'SiBitcoin', labelKey: 'Bitcoin', Icon: SiBitcoin },
  { name: 'SiEthereum', labelKey: 'Ethereum', Icon: SiEthereum },
  { name: 'SiTether', labelKey: 'Tether (USDT)', Icon: SiTether },
  { name: 'FaWallet', labelKey: 'Wallet', Icon: FaWallet },
  { name: 'FaCoins', labelKey: 'Coins', Icon: FaCoins },
  { name: 'FaMoneyBillWave', labelKey: 'Cash', Icon: FaMoneyBillWave },
  { name: 'FaQrcode', labelKey: 'QR code', Icon: FaQrcode },
]

/** The curated icon components by stored name. */
export const PAYMENT_ICON_BY_NAME: ReadonlyMap<string, IconType> = new Map(
  PAYMENT_ICONS.map((icon) => [icon.name, icon.Icon])
)
