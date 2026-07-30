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
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { quotaUnitsToDollars } from '@/lib/format'
import {
  MAX_QUOTA_PER_REQUEST,
  MAX_WINDOW_QUOTA,
  TOKEN_RISK_MODES,
  tokenRiskModeRank,
  type TokenRiskMode,
  type TokenSecurityPolicyView,
} from '@/lib/token-security-policy'

import type { ApiKeyFormValues } from '../lib'
import { AdministratorTokenSecuritySummary } from './api-key-security-policy-summary'

type UserSecurityNumberFieldName =
  | 'max_quota_per_request'
  | 'hourly_quota'
  | 'daily_quota'

type ApiKeySecurityPolicyFieldsProps = {
  form: UseFormReturn<ApiKeyFormValues>
  policyView: TokenSecurityPolicyView | null
}

const RISK_MODE_LABEL_KEYS: Record<TokenRiskMode, string> = {
  observe: 'Audit only',
  notify: 'Audit and notify',
  suspend: 'Automatically suspend token',
}

function effectiveUserLimit(requested: number, administratorMaximum: number) {
  if (requested <= 0) return administratorMaximum
  if (administratorMaximum <= 0) return requested
  return Math.min(requested, administratorMaximum)
}

function inheritedLimitDescription(
  requested: number,
  administratorMaximum: number,
  t: TFunction
): string {
  const maximum = administratorMaximum || t('unlimited')
  const effective =
    effectiveUserLimit(requested, administratorMaximum) || t('unlimited')
  return t(
    '0 inherits the administrator maximum ({{maximum}}). Effective value: {{effective}}.',
    { maximum, effective }
  )
}

function UserSecurityNumberField(props: {
  form: UseFormReturn<ApiKeyFormValues>
  administratorProfile: TokenSecurityPolicyView['admin_profile'] | null
  name: UserSecurityNumberFieldName
  label: string
  max: number
  tokensOnly: boolean
}) {
  const { t } = useTranslation()
  const administratorMaximum = quotaUnitsToDollars(
    props.administratorProfile?.[props.name] ?? 0
  )

  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={0}
              max={props.max}
              step={props.tokensOnly ? 1 : 'any'}
              {...field}
              onChange={(event) =>
                field.onChange(Number.parseFloat(event.target.value) || 0)
              }
            />
          </FormControl>
          <FormDescription>
            {props.administratorProfile
              ? inheritedLimitDescription(field.value, administratorMaximum, t)
              : t('0 inherits the administrator policy')}
          </FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

export function ApiKeySecurityPolicyFields(
  props: ApiKeySecurityPolicyFieldsProps
) {
  const { t } = useTranslation()
  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const minimumRiskMode =
    props.policyView?.admin_profile.minimum_risk_mode ?? 'observe'

  return (
    <div className='space-y-4'>
      {props.policyView && (
        <AdministratorTokenSecuritySummary view={props.policyView} />
      )}

      <div className='grid gap-4 sm:grid-cols-2'>
        <UserSecurityNumberField
          form={props.form}
          administratorProfile={props.policyView?.admin_profile ?? null}
          name='max_quota_per_request'
          label={`${t('Maximum quota per request')} (${currencyLabel})`}
          max={quotaUnitsToDollars(MAX_QUOTA_PER_REQUEST)}
          tokensOnly={tokensOnly}
        />
        <UserSecurityNumberField
          form={props.form}
          administratorProfile={props.policyView?.admin_profile ?? null}
          name='hourly_quota'
          label={`${t('Maximum hourly quota')} (${currencyLabel})`}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          tokensOnly={tokensOnly}
        />
        <UserSecurityNumberField
          form={props.form}
          administratorProfile={props.policyView?.admin_profile ?? null}
          name='daily_quota'
          label={`${t('Maximum daily quota')} (${currencyLabel})`}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          tokensOnly={tokensOnly}
        />
      </div>

      <FormField
        control={props.form.control}
        name='risk_mode'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Risk response')}</FormLabel>
            <Select value={field.value} onValueChange={field.onChange}>
              <FormControl>
                <SelectTrigger className='w-full'>
                  <SelectValue>
                    {t(RISK_MODE_LABEL_KEYS[field.value])}
                  </SelectValue>
                </SelectTrigger>
              </FormControl>
              <SelectContent>
                <SelectGroup>
                  {TOKEN_RISK_MODES.map((mode) => (
                    <SelectItem
                      key={mode}
                      value={mode}
                      disabled={
                        tokenRiskModeRank(mode) <
                        tokenRiskModeRank(minimumRiskMode)
                      }
                    >
                      {t(RISK_MODE_LABEL_KEYS[mode])}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <FormDescription>
              {t(
                'Administrator minimum: {{mode}}. Only the affected API key is suspended.',
                {
                  mode: t(RISK_MODE_LABEL_KEYS[minimumRiskMode]),
                }
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  )
}
