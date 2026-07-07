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
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import {
  REDEMPTION_VALIDATION,
  getRedemptionFormErrorMessages,
} from '../constants'
import { type RedemptionFormData, type Redemption } from '../types'

// ============================================================================
// Form Schema (use getRedemptionFormSchema(t) in components for i18n messages)
// ============================================================================

export function getRedemptionFormSchema(t: TFunction) {
  const msg = getRedemptionFormErrorMessages(t)
  return z
    .object({
      name: z
        .string()
        .min(REDEMPTION_VALIDATION.NAME_MIN_LENGTH, msg.NAME_LENGTH_INVALID)
        .max(REDEMPTION_VALIDATION.NAME_MAX_LENGTH, msg.NAME_LENGTH_INVALID),
      quota_type: z.enum(['recharge', 'gift']),
      amount_dollars: z.number().min(0, t('Quota cannot be negative')),
      expired_time: z.date().optional(),
      count: z
        .number()
        .min(REDEMPTION_VALIDATION.COUNT_MIN, msg.COUNT_INVALID)
        .max(REDEMPTION_VALIDATION.COUNT_MAX, msg.COUNT_INVALID)
        .optional(),
    })
    .refine((data) => data.amount_dollars > 0, {
      message: t('Amount must be greater than 0'),
      path: ['amount_dollars'],
    })
}

export type RedemptionQuotaType = 'recharge' | 'gift'

export type RedemptionFormValues = {
  name: string
  quota_type: RedemptionQuotaType
  amount_dollars: number
  expired_time?: Date
  count?: number
}

// ============================================================================
// Form Defaults
// ============================================================================

export const REDEMPTION_FORM_DEFAULT_VALUES: RedemptionFormValues = {
  name: '',
  quota_type: 'recharge',
  amount_dollars: 10,
  expired_time: undefined,
  count: 1,
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: RedemptionFormValues
): RedemptionFormData {
  const quota = parseQuotaFromDollars(data.amount_dollars)
  return {
    name: data.name,
    quota: data.quota_type === 'recharge' ? quota : 0,
    gift_quota: data.quota_type === 'gift' ? quota : 0,
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : 0,
    count: data.count || 1,
  }
}

/**
 * Transform redemption data to form defaults
 */
export function transformRedemptionToFormDefaults(
  redemption: Redemption
): RedemptionFormValues {
  const giftQuota = redemption.gift_quota ?? 0
  const quotaType: RedemptionQuotaType =
    giftQuota > 0 && redemption.quota <= 0 ? 'gift' : 'recharge'
  const amount = quotaType === 'gift' ? giftQuota : redemption.quota
  return {
    name: redemption.name,
    quota_type: quotaType,
    amount_dollars: quotaUnitsToDollars(amount),
    expired_time:
      redemption.expired_time > 0
        ? new Date(redemption.expired_time * 1000)
        : undefined,
    count: 1,
  }
}
