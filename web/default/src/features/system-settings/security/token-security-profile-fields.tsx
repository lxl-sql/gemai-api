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
import {
  MAX_QUOTA_PER_REQUEST,
  MAX_WINDOW_QUOTA,
} from '@/lib/token-security-policy'
import type { TokenSecurityProfileValues } from '@/lib/token-security-profile-form'

import {
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'

type ProfileNumberFieldName =
  | 'sustained_rps'
  | 'burst_capacity'
  | 'max_concurrency'
  | 'max_quota_per_request'
  | 'hourly_quota'
  | 'daily_quota'
  | 'max_distinct_models_5m'

function PolicyNumberField(props: {
  form: UseFormReturn<TokenSecurityProfileValues>
  name: ProfileNumberFieldName
  label: string
  description: string
  max?: number
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
              step={1}
              {...field}
              onChange={(event) =>
                field.onChange(Number.parseInt(event.target.value, 10) || 0)
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

  return (
    <>
      <SettingsFormGrid>
        <PolicyNumberField
          form={props.form}
          name='sustained_rps'
          label={t('Maximum sustained requests/second')}
          description={t('0 means no administrator maximum')}
          max={100000}
        />
        <PolicyNumberField
          form={props.form}
          name='burst_capacity'
          label={t('Maximum burst capacity')}
          description={t('0 means no administrator maximum')}
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
          label={t('Maximum quota per request')}
          description={t('Quota units; 0 means no administrator maximum')}
          max={MAX_QUOTA_PER_REQUEST}
        />
        <PolicyNumberField
          form={props.form}
          name='hourly_quota'
          label={t('Maximum hourly quota')}
          description={t('Quota units; 0 means no administrator maximum')}
          max={MAX_WINDOW_QUOTA}
        />
        <PolicyNumberField
          form={props.form}
          name='daily_quota'
          label={t('Maximum daily quota')}
          description={t('Quota units; 0 means no administrator maximum')}
          max={MAX_WINDOW_QUOTA}
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
