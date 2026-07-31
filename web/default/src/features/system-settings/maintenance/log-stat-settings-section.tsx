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
import { useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import type { SystemOptionsResponse } from '../types'
import { safeNumberFieldProps } from '../utils/numeric-field'

const logStatSettingsSchema = z.object({
  log_stat_setting: z.object({
    enabled: z.boolean(),
    backfill_enabled: z.boolean(),
    recent_minutes: z.coerce.number().int().min(5).max(30),
  }),
})

type LogStatSettingsInput = z.input<typeof logStatSettingsSchema>
type LogStatSettingsValues = z.output<typeof logStatSettingsSchema>

export type LogStatSettingsDefaults = {
  'log_stat_setting.enabled': boolean
  'log_stat_setting.backfill_enabled': boolean
  'log_stat_setting.recent_minutes': number
}

type LogStatSettingsSectionProps = {
  defaultValues: LogStatSettingsDefaults
}

function buildFormDefaults(
  defaults: LogStatSettingsDefaults
): LogStatSettingsInput {
  return {
    log_stat_setting: {
      enabled: defaults['log_stat_setting.enabled'],
      backfill_enabled: defaults['log_stat_setting.backfill_enabled'],
      recent_minutes: defaults['log_stat_setting.recent_minutes'],
    },
  }
}

function flattenFormValues(
  values: LogStatSettingsValues
): LogStatSettingsDefaults {
  return {
    'log_stat_setting.enabled': values.log_stat_setting.enabled,
    'log_stat_setting.backfill_enabled':
      values.log_stat_setting.backfill_enabled,
    'log_stat_setting.recent_minutes':
      values.log_stat_setting.recent_minutes,
  }
}

export function LogStatSettingsSection(props: LogStatSettingsSectionProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption({
    skipInvalidate: true,
    skipSuccessToast: true,
  })
  const formDefaults = buildFormDefaults(props.defaultValues)
  const form = useForm<LogStatSettingsInput, unknown, LogStatSettingsValues>({
    resolver: zodResolver(logStatSettingsSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: LogStatSettingsValues) => {
    const normalized = flattenFormValues(values)
    const changedKeys = (
      Object.keys(normalized) as Array<keyof LogStatSettingsDefaults>
    ).filter((key) => normalized[key] !== props.defaultValues[key])

    const savedValues: Partial<LogStatSettingsDefaults> = {}
    let saveFailed = false
    try {
      for (const key of changedKeys) {
        const result = await updateOption.mutateAsync({
          key,
          value: normalized[key],
        })
        if (!result.success) {
          saveFailed = true
          break
        }
        Object.assign(savedValues, { [key]: normalized[key] })
      }
    } catch {
      // useUpdateOption already reports the request error. Restore the form
      // below to values whose successful responses were confirmed.
      saveFailed = true
    } finally {
      if (Object.keys(savedValues).length > 0) {
        queryClient.setQueryData<SystemOptionsResponse>(
          ['system-options'],
          (previous) => {
            if (!previous?.data) return previous
            const remaining = Object.fromEntries(
              Object.entries(savedValues).map(([key, value]) => [
                key,
                String(value),
              ])
            )
            const data = previous.data.map((option) => {
              const value = remaining[option.key]
              if (value === undefined) return option
              delete remaining[option.key]
              return { ...option, value }
            })
            for (const [key, value] of Object.entries(remaining)) {
              data.push({ key, value })
            }
            return { ...previous, data }
          }
        )
      }
    }
    if (saveFailed) {
      form.reset(
        buildFormDefaults({
          ...props.defaultValues,
          ...savedValues,
        })
      )
      // Do not refetch immediately: another cluster node may not have received
      // the option sync yet and could overwrite confirmed values with stale
      // data. Mark the snapshot stale for the next normal load instead.
      await queryClient.invalidateQueries({
        queryKey: ['system-options'],
        refetchType: 'none',
      })
      return
    }
    if (changedKeys.length > 0) {
      toast.success(t('Setting updated successfully'))
    }
  }

  const aggregationEnabled = form.watch('log_stat_setting.enabled')

  return (
    <SettingsSection title={t('Log statistics aggregation')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log statistics settings'
          />

          <FormField
            control={form.control}
            name='log_stat_setting.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable log statistics aggregation')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Use minute-level pre-aggregation for log usage statistics. Disabling it makes statistics unavailable.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_stat_setting.backfill_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Backfill historical log statistics')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Fill the retained historical range gradually in small background batches.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={!aggregationEnabled}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_stat_setting.recent_minutes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Recent replay window (minutes)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={5}
                    max={30}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Rebuild this recent window every minute to absorb delayed logs and master failover. Recommended: 5 minutes.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
