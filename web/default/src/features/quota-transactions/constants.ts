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
import type { TFunction } from 'i18next'

export const QUOTA_TRANSACTION_TYPES = [
  'topup',
  'gift',
  'redemption',
  'refund',
  'admin_adjust',
  'subscription_buy',
  'subscription_grant',
  'checkin',
] as const

export const QUOTA_TRANSACTION_TYPE_VARIANTS: Record<
  string,
  'success' | 'warning' | 'danger' | 'neutral' | 'info'
> = {
  topup: 'success',
  gift: 'success',
  redemption: 'success',
  refund: 'success',
  admin_adjust: 'info',
  subscription_buy: 'danger',
  subscription_grant: 'success',
  checkin: 'success',
}

export function getQuotaTransactionTypeLabelKey(type: string) {
  const labels: Record<string, string> = {
    topup: 'Top up',
    gift: 'Gift',
    redemption: 'Redemption',
    refund: 'Refund',
    admin_adjust: 'Admin adjustment',
    subscription_buy: 'Subscription purchase',
    subscription_grant: 'Subscription grant',
    checkin: 'Check-in',
  }
  return labels[type] ?? type
}

export function getQuotaTransactionSourceLabelKey(source: string): string {
  const labels: Record<string, string> = {
    redemption: 'Redemption',
    relay: 'API Call',
    admin: 'Admin',
    legacy: 'Legacy Data',
    system: 'System',
    invite: 'Invitation Reward',
    register: 'Registration Gift',
    checkin: 'Check-in',
    subscription: 'Subscription',
    stripe: 'Stripe Top up',
    epay: 'Epay Top up',
    creem: 'Creem Top up',
    waffo: 'Waffo Top up',
    waffo_pancake: 'Waffo Pancake Top up',
  }
  return labels[source] ?? source
}

export function getQuotaTransactionReferenceTypeLabelKey(refType: string): string {
  const labels: Record<string, string> = {
    redemption: 'Redemption',
    request: 'Request',
    user: 'User',
    manual: 'Manual',
    subscription: 'Subscription',
    legacy_user_gift_quota: 'Legacy Gift Quota',
  }
  return labels[refType] ?? refType
}

export function getDirectionOptions(t: TFunction) {
  return [
    { label: t('Income'), value: 'income' },
    { label: t('Expense'), value: 'expense' },
  ]
}

export function getBucketOptions(t: TFunction) {
  return [
    { label: t('Recharge Quota'), value: 'recharge' },
    { label: t('Gift Quota'), value: 'gift' },
    { label: t('Both quotas'), value: 'both' },
  ]
}

export function getTypeOptions(t: TFunction) {
  return QUOTA_TRANSACTION_TYPES.map((value) => ({
    label: t(getQuotaTransactionTypeLabelKey(value)),
    value,
  }))
}
