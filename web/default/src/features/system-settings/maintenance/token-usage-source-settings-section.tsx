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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
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
import { safeNumberFieldProps } from '../utils/numeric-field'

const usageSourceSettingsSchema = z.object({
  token_usage_source_setting: z.object({
    enabled: z.boolean(),
    reconcile_enabled: z.boolean(),
    backfill_enabled: z.boolean(),
    backfill_days: z.coerce.number().int().min(1).max(3650),
    max_sources_per_token: z.coerce.number().int().min(10).max(5000),
  }),
})

type UsageSourceSettingsFormInput = z.input<typeof usageSourceSettingsSchema>
type UsageSourceSettingsFormValues = z.output<typeof usageSourceSettingsSchema>

export type TokenUsageSourceSettingsDefaults = {
  'token_usage_source_setting.enabled': boolean
  'token_usage_source_setting.reconcile_enabled': boolean
  'token_usage_source_setting.backfill_enabled': boolean
  'token_usage_source_setting.backfill_days': number
  'token_usage_source_setting.max_sources_per_token': number
}

type TokenUsageSourceSettingsSectionProps = {
  defaultValues: TokenUsageSourceSettingsDefaults
}

function buildFormDefaults(
  defaults: TokenUsageSourceSettingsDefaults
): UsageSourceSettingsFormInput {
  return {
    token_usage_source_setting: {
      enabled: defaults['token_usage_source_setting.enabled'],
      reconcile_enabled:
        defaults['token_usage_source_setting.reconcile_enabled'],
      backfill_enabled: defaults['token_usage_source_setting.backfill_enabled'],
      backfill_days: defaults['token_usage_source_setting.backfill_days'],
      max_sources_per_token:
        defaults['token_usage_source_setting.max_sources_per_token'],
    },
  }
}

function flattenFormValues(
  values: UsageSourceSettingsFormValues
): TokenUsageSourceSettingsDefaults {
  return {
    'token_usage_source_setting.enabled':
      values.token_usage_source_setting.enabled,
    'token_usage_source_setting.reconcile_enabled':
      values.token_usage_source_setting.reconcile_enabled,
    'token_usage_source_setting.backfill_enabled':
      values.token_usage_source_setting.backfill_enabled,
    'token_usage_source_setting.backfill_days':
      values.token_usage_source_setting.backfill_days,
    'token_usage_source_setting.max_sources_per_token':
      values.token_usage_source_setting.max_sources_per_token,
  }
}

export function TokenUsageSourceSettingsSection(
  props: TokenUsageSourceSettingsSectionProps
) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const formDefaults = buildFormDefaults(props.defaultValues)
  const form = useForm<
    UsageSourceSettingsFormInput,
    unknown,
    UsageSourceSettingsFormValues
  >({
    resolver: zodResolver(usageSourceSettingsSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: UsageSourceSettingsFormValues) => {
    const normalized = flattenFormValues(values)
    const changedKeys = (
      Object.keys(normalized) as Array<keyof TokenUsageSourceSettingsDefaults>
    ).filter((key) => normalized[key] !== props.defaultValues[key])

    for (const key of changedKeys) {
      await updateOption.mutateAsync({
        key,
        value: normalized[key],
      })
    }
  }

  const aggregationEnabled = form.watch('token_usage_source_setting.enabled')

  return (
    <SettingsSection title={t('API Key Usage Sources')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save usage source settings'
          />

          <FormField
            control={form.control}
            name='token_usage_source_setting.reconcile_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Reconcile API Key source metadata')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Repair missing or stale source metadata in small background batches before enabling aggregation.'
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
            name='token_usage_source_setting.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable API Key usage sources')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Aggregate recent client IP and user agent combinations and show the Usage Sources action.'
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
            name='token_usage_source_setting.backfill_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Backfill historical usage sources')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Process older logs gradually. Keep this off until realtime aggregation is stable.'
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
            name='token_usage_source_setting.backfill_days'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Historical backfill days')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={3650}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t('Limit how far back the background task scans logs.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='token_usage_source_setting.max_sources_per_token'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Maximum sources per API Key')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={10}
                    max={5000}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Keep only the most recently seen deduplicated sources for each API Key.'
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
