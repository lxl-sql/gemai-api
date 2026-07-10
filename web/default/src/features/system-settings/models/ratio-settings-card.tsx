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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { resetModelRatios } from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { SystemOptionsResponse } from '../types'
import { GroupRatioForm } from './group-ratio-form'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let location: string
  if (error.line && error.column) {
    location = t('JSON is invalid at line {{line}}, column {{column}}.', {
      line: error.line,
      column: error.column,
    })
  } else if (error.position !== undefined) {
    location = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    location = t('JSON is invalid. Please check the syntax.')
  }
  const parts = [location]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    TopupGroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
    GroupGroupRatio: createJsonStringField(t),
    AutoGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: createJsonStringField(t),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>
type RatioTabId = 'models' | 'groups' | 'tool-prices' | 'upstream-sync'

function normalizeModelValues(values: ModelFormValues) {
  return {
    ModelPrice: normalizeJsonString(values.ModelPrice),
    ModelRatio: normalizeJsonString(values.ModelRatio),
    CacheRatio: normalizeJsonString(values.CacheRatio),
    CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(values.CompletionRatio),
    ImageRatio: normalizeJsonString(values.ImageRatio),
    AudioRatio: normalizeJsonString(values.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
    ExposeRatioEnabled: values.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(values.BillingMode),
    BillingExpr: normalizeJsonString(values.BillingExpr),
  }
}

function normalizeGroupValues(values: GroupFormValues) {
  return {
    GroupRatio: normalizeJsonString(values.GroupRatio),
    TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
    GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
    AutoGroups: normalizeJsonString(values.AutoGroups),
    DefaultUseAutoGroup: values.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      values.GroupSpecialUsableGroup
    ),
  }
}

function valuesDiffer<T extends Record<string, string | boolean>>(
  a: T,
  b: T
): boolean {
  return (Object.keys(a) as Array<keyof T>).some((key) => a[key] !== b[key])
}

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

export function RatioSettingsCard({
  modelDefaults,
  groupDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  // This card saves several related keys sequentially; per-key invalidation
  // would refetch mid-batch and can resurrect stale values, and per-key
  // toasts stack up. The save handlers below update the query cache and
  // show one summary toast once the whole batch succeeds.
  const updateOption = useUpdateOption({
    skipInvalidate: true,
    skipSuccessToast: true,
  })
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  // Set when server state must overwrite local form state even if the form
  // has unsaved edits (e.g. after "Reset prices").
  const forceServerSyncRef = useRef(false)

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: (data) => {
      if (data.success) {
        toast.success(t('Model prices reset successfully'))
        forceServerSyncRef.current = true
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
        setConfirmOpen(false)
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to reset model ratios'))
    },
  })

  const modelNormalizedDefaults = useRef(normalizeModelValues(modelDefaults))
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )

  const groupNormalizedDefaults = useRef(normalizeGroupValues(groupDefaults))
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    },
  })

  useEffect(() => {
    const next = normalizeModelValues(modelDefaults)

    // `modelDefaults` gets a fresh identity on every parent render, so only
    // react to actual value changes; a blind reset here wipes user drafts.
    if (!valuesDiffer(next, modelNormalizedDefaults.current)) return

    // Never let a background refetch (which may carry a stale snapshot from
    // another instance) clobber in-progress edits or a save that is mid-flight.
    if (
      !forceServerSyncRef.current &&
      (updateOption.isPending || modelForm.formState.isDirty)
    ) {
      return
    }
    forceServerSyncRef.current = false

    modelNormalizedDefaults.current = next
    setSavedModelValues(next)

    modelForm.reset({
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    })
  }, [modelDefaults, modelForm, updateOption.isPending])

  useEffect(() => {
    const next = normalizeGroupValues(groupDefaults)

    if (!valuesDiffer(next, groupNormalizedDefaults.current)) return
    if (updateOption.isPending || groupForm.formState.isDirty) return

    groupNormalizedDefaults.current = next

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    })
  }, [groupDefaults, groupForm, updateOption.isPending])

  // After a successful batch save, place the values we just wrote into the
  // query cache instead of refetching. A refetch right after the write can
  // return a stale snapshot (in-flight GET or a not-yet-synced instance) and
  // silently revert the save.
  const writeOptionsToCache = useCallback(
    (entries: Record<string, string>) => {
      queryClient.setQueryData<SystemOptionsResponse>(
        ['system-options'],
        (prev) => {
          if (!prev?.data) return prev
          const remaining = { ...entries }
          const data = prev.data.map((option) => {
            const value = remaining[option.key]
            if (value === undefined) return option
            delete remaining[option.key]
            return { ...option, value }
          })
          for (const [key, value] of Object.entries(remaining)) {
            data.push({ key, value })
          }
          return { ...prev, data }
        }
      )
    },
    [queryClient]
  )

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = normalizeModelValues(values)

      const apiKeyMap: Record<string, string> = {
        BillingMode: 'billing_setting.billing_mode',
        BillingExpr: 'billing_setting.billing_expr',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof ModelFormValues>
      ).filter(
        (key) => normalized[key] !== modelNormalizedDefaults.current[key]
      )

      if (updates.length === 0) {
        toast.info(t('No model price changes to save'))
        return
      }

      const savedEntries: Record<string, string> = {}
      try {
        for (const key of updates) {
          const apiKey = apiKeyMap[key as string] || (key as string)
          const result = await updateOption.mutateAsync({
            key: apiKey,
            value: normalized[key],
          })
          // Business failure: the hook already toasts; stop so the local
          // baseline never records a value the server rejected. The form
          // stays dirty and the user can retry.
          if (!result.success) return
          savedEntries[apiKey] = String(normalized[key])
        }
      } finally {
        // Keep the cache in sync with whatever actually reached the server,
        // even when a later key in the batch fails.
        if (Object.keys(savedEntries).length > 0) {
          writeOptionsToCache(savedEntries)
        }
      }

      modelNormalizedDefaults.current = normalized
      setSavedModelValues(normalized)
      // Clear dirty state so background refetches may sync again, while
      // keeping whatever is currently typed in the editors.
      modelForm.reset(values, { keepValues: true })
      toast.success(t('Setting updated successfully'))
    },
    [t, updateOption, modelForm, writeOptionsToCache]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized = normalizeGroupValues(values)

      // Map form field names to API keys (most are 1:1, except GroupSpecialUsableGroup)
      const apiKeyMap: Record<string, string> = {
        GroupSpecialUsableGroup:
          'group_ratio_setting.group_special_usable_group',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof typeof normalized>
      ).filter(
        (key) => normalized[key] !== groupNormalizedDefaults.current[key]
      )

      if (updates.length === 0) return

      const savedEntries: Record<string, string> = {}
      try {
        for (const key of updates) {
          const apiKey = apiKeyMap[key] || key
          const result = await updateOption.mutateAsync({
            key: apiKey,
            value: normalized[key],
          })
          if (!result.success) return
          savedEntries[apiKey] = String(normalized[key])
        }
      } finally {
        if (Object.keys(savedEntries).length > 0) {
          writeOptionsToCache(savedEntries)
        }
      }

      groupNormalizedDefaults.current = normalized
      groupForm.reset(values, { keepValues: true })
      toast.success(t('Setting updated successfully'))
    },
    [t, updateOption, groupForm, writeOptionsToCache]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    groups: 'Group ratios',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models') {
      return (
        <ModelRatioForm
          form={modelForm}
          savedValues={savedModelValues}
          onSave={saveModelRatios}
          onReset={handleResetRatios}
          isSaving={updateOption.isPending}
          isResetting={resetMutation.isPending}
        />
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          onSave={saveGroupRatios}
          isSaving={updateOption.isPending}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return (
      <UpstreamRatioSync
        modelRatios={{
          ModelPrice: modelDefaults.ModelPrice,
          ModelRatio: modelDefaults.ModelRatio,
          CompletionRatio: modelDefaults.CompletionRatio,
          CacheRatio: modelDefaults.CacheRatio,
          CreateCacheRatio: modelDefaults.CreateCacheRatio,
          ImageRatio: modelDefaults.ImageRatio,
          AudioRatio: modelDefaults.AudioRatio,
          AudioCompletionRatio: modelDefaults.AudioCompletionRatio,
          'billing_setting.billing_mode': modelDefaults.BillingMode,
          'billing_setting.billing_expr': modelDefaults.BillingExpr,
        }}
      />
    )
  }

  const renderTabSwitcher = () => (
    <TabsList className={`grid w-fit max-w-full ${tabsGridClass}`}>
      {visibleTabs.map((tab) => (
        <TabsTrigger key={tab} value={tab}>
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <SettingsSection title={t(titleKey)}>
          {renderTabContent(defaultTab)}
        </SettingsSection>
      ) : (
        <Tabs defaultValue={defaultTab} className='space-y-6'>
          <SettingsPageTitleStatusPortal>
            {renderTabSwitcher()}
          </SettingsPageTitleStatusPortal>

          <SettingsSection title={t(titleKey)}>
            {visibleTabs.map((tab) => (
              <TabsContent key={tab} value={tab}>
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </SettingsSection>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
