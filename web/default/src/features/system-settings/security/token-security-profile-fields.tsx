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
import { Switch } from '@/components/ui/switch'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { quotaUnitsToDollars } from '@/lib/format'
import {
  MAX_QUOTA_PER_REQUEST,
  MAX_WINDOW_QUOTA,
} from '@/lib/token-security-policy'
import type { TokenSecurityProfileValues } from '@/lib/token-security-profile-form'

import {
  SettingsFormGrid,
  SettingsFormGridItem,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'

type ProfileNumberFieldName =
  | 'sustained_rps'
  | 'sustained_rpm'
  | 'burst_capacity'
  | 'max_concurrency'
  | 'max_quota_per_request'
  | 'hourly_quota'
  | 'daily_quota'
  | 'max_distinct_models_5m'
  | 'user_sustained_rpm'
  | 'user_burst_capacity'
  | 'user_max_concurrency'
  | 'user_hourly_quota'
  | 'user_daily_quota'

function PolicyNumberField(props: {
  form: UseFormReturn<TokenSecurityProfileValues>
  name: ProfileNumberFieldName
  label: string
  description: string
  max?: number
  decimal?: boolean
}) {
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
              step={props.decimal ? 'any' : 1}
              {...field}
              onChange={(event) =>
                field.onChange(
                  props.decimal
                    ? Number.parseFloat(event.target.value) || 0
                    : Number.parseInt(event.target.value, 10) || 0
                )
              }
            />
          </FormControl>
          <FormDescription>{props.description}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

function riskModeItems(t: TFunction) {
  return [
    { value: 'observe', label: t('Audit only') },
    { value: 'notify', label: t('Audit and notify') },
    { value: 'suspend', label: t('Automatically suspend token') },
  ]
}

export function TokenSecurityProfileFields(props: {
  form: UseFormReturn<TokenSecurityProfileValues>
}) {
  const { t } = useTranslation()
  const riskItems = riskModeItems(t)
  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const quotaAllowsDecimals = currencyMeta.kind !== 'tokens'

  return (
    <>
      <SettingsFormGrid>
        <PolicyNumberField
          form={props.form}
          name='sustained_rps'
          label={t('Maximum sustained requests/second')}
          description={t(
            '0 disables RPS. RPM takes precedence when configured.'
          )}
          max={100000}
        />
        <PolicyNumberField
          form={props.form}
          name='sustained_rpm'
          label={t('Maximum sustained requests/minute')}
          description={t('0 disables RPM and uses the RPS limit instead.')}
          max={6000000}
        />
        <PolicyNumberField
          form={props.form}
          name='burst_capacity'
          label={t('Maximum burst capacity')}
          description={t(
            '0 uses an automatic burst capacity when a request rate is configured.'
          )}
          max={1000000}
        />
        <PolicyNumberField
          form={props.form}
          name='max_concurrency'
          label={t('Maximum concurrency')}
          description={t('0 means no administrator maximum')}
          max={1000000}
        />
        <PolicyNumberField
          form={props.form}
          name='max_distinct_models_5m'
          label={t('Maximum distinct models per 5 minutes')}
          description={t('0 means no administrator maximum')}
          max={10000}
        />
        <PolicyNumberField
          form={props.form}
          name='max_quota_per_request'
          label={`${t('Maximum quota per request')} (${currencyLabel})`}
          description={t('0 means no administrator maximum')}
          max={quotaUnitsToDollars(MAX_QUOTA_PER_REQUEST)}
          decimal={quotaAllowsDecimals}
        />
        <PolicyNumberField
          form={props.form}
          name='hourly_quota'
          label={`${t('Maximum hourly quota')} (${currencyLabel})`}
          description={t('0 means no administrator maximum')}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          decimal={quotaAllowsDecimals}
        />
        <PolicyNumberField
          form={props.form}
          name='daily_quota'
          label={`${t('Maximum daily quota')} (${currencyLabel})`}
          description={t('0 means no administrator maximum')}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          decimal={quotaAllowsDecimals}
        />

        <SettingsFormGridItem span='full' className='space-y-1 border-t pt-5'>
          <h4 className='text-sm font-medium'>{t('Shared user limits')}</h4>
          <p className='text-muted-foreground text-xs'>
            {t(
              'These limits are shared by all API keys owned by the same user.'
            )}
          </p>
        </SettingsFormGridItem>

        <PolicyNumberField
          form={props.form}
          name='user_sustained_rpm'
          label={t('Maximum user requests/minute')}
          description={t('0 means no administrator maximum')}
          max={6000000}
        />
        <PolicyNumberField
          form={props.form}
          name='user_burst_capacity'
          label={t('Maximum user burst capacity')}
          description={t(
            '0 uses an automatic burst capacity when a request rate is configured.'
          )}
          max={1000000}
        />
        <PolicyNumberField
          form={props.form}
          name='user_max_concurrency'
          label={t('Maximum user concurrency')}
          description={t('0 means no administrator maximum')}
          max={1000000}
        />
        <PolicyNumberField
          form={props.form}
          name='user_hourly_quota'
          label={`${t('Maximum user hourly quota')} (${currencyLabel})`}
          description={t('0 means no administrator maximum')}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          decimal={quotaAllowsDecimals}
        />
        <PolicyNumberField
          form={props.form}
          name='user_daily_quota'
          label={`${t('Maximum user daily quota')} (${currencyLabel})`}
          description={t('0 means no administrator maximum')}
          max={quotaUnitsToDollars(MAX_WINDOW_QUOTA)}
          decimal={quotaAllowsDecimals}
        />

        <FormField
          control={props.form.control}
          name='minimum_risk_mode'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Minimum risk response')}</FormLabel>
              <Select
                items={riskItems}
                value={field.value}
                onValueChange={(value) => {
                  if (value) field.onChange(value)
                }}
              >
                <FormControl>
                  <SelectTrigger className='w-full'>
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectGroup>
                    {riskItems.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {item.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FormDescription>
                {t('API key owners may select a stricter response only.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </SettingsFormGrid>

      <FormField
        control={props.form.control}
        name='fail_closed'
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>
                {t('Require fail closed when Redis is unavailable')}
              </FormLabel>
              <FormDescription>
                {t(
                  'Enable only when Redis is highly available. API key owners can opt in when this is off, but cannot opt out when it is on.'
                )}
              </FormDescription>
            </SettingsSwitchContent>
            <FormControl>
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            </FormControl>
          </SettingsSwitchItem>
        )}
      />
    </>
  )
}
