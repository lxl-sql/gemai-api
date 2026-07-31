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
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, Search, X } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { getPricing } from '@/features/pricing/api'
import { cn } from '@/lib/utils'

const FILTER_ALL = 'all'
const VENDOR_UNKNOWN = '__unknown__'
const BILLING_TOKEN = 'token'
const BILLING_REQUEST = 'request'

/** Matches `quota_type` on the pricing API. */
const QUOTA_TYPE_TOKEN = 0
const QUOTA_TYPE_REQUEST = 1

/** Bulk selection can reach hundreds of models; keep the chip row bounded. */
const MAX_VISIBLE_CHIPS = 12

type ModelEntry = {
  name: string
  vendor: string
  /** `undefined` when the model has no pricing metadata. */
  quotaType?: number
}

type FilterOption = {
  value: string
  label: string
  count: number
}

function matchesBilling(entry: ModelEntry, filter: string): boolean {
  if (filter === BILLING_TOKEN) return entry.quotaType === QUOTA_TYPE_TOKEN
  if (filter === BILLING_REQUEST) return entry.quotaType === QUOTA_TYPE_REQUEST
  return true
}

function matchesVendor(entry: ModelEntry, filter: string): boolean {
  if (filter === VENDOR_UNKNOWN) return !entry.vendor
  if (filter === FILTER_ALL) return true
  return entry.vendor === filter
}

function FilterRow(props: {
  label: string
  options: FilterOption[]
  value: string
  onChange: (value: string) => void
}) {
  return (
    <div className='flex items-start gap-2'>
      <span className='text-muted-foreground shrink-0 pt-1 text-xs'>
        {props.label}
      </span>
      <div className='flex flex-1 flex-wrap gap-1.5'>
        {props.options.map((option) => {
          const active = option.value === props.value
          return (
            <button
              key={option.value}
              type='button'
              onClick={() => props.onChange(option.value)}
              title={option.label}
              className={cn(
                'inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors',
                active
                  ? 'border-foreground/30 bg-foreground/5 text-foreground'
                  : 'border-border/70 bg-background text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground'
              )}
            >
              <span className='truncate'>{option.label}</span>
              <span
                className={cn(
                  'rounded-md px-1.5 py-0.5 text-[10px]',
                  active
                    ? 'bg-background text-foreground'
                    : 'bg-muted text-muted-foreground'
                )}
              >
                {option.count}
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}

type ModelLimitsPickerProps = {
  /** Models the user may call, from `/api/user/models`. */
  models: string[]
  value: string[]
  onChange: (next: string[]) => void
  disabled?: boolean
  /** Injected by `FormControl` so the field label and messages reach the trigger. */
  id?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

/**
 * Model restriction picker: search + billing/vendor filters + bulk selection.
 *
 * Vendor and billing metadata comes from `/api/pricing`, which an admin can
 * disable per site. When it is unavailable the picker degrades to a searchable
 * checkbox list, so model selection never depends on that endpoint.
 */
export function ModelLimitsPicker(props: ModelLimitsPickerProps) {
  const { t } = useTranslation()
  const uid = useId()
  const [expanded, setExpanded] = useState(false)
  const [search, setSearch] = useState('')
  const [billingFilter, setBillingFilter] = useState<string>(FILTER_ALL)
  const [vendorFilter, setVendorFilter] = useState<string>(FILTER_ALL)

  const { data: pricing } = useQuery({
    queryKey: ['pricing'],
    queryFn: getPricing,
    staleTime: 5 * 60 * 1000,
    enabled: expanded,
    retry: false,
  })

  const entries = useMemo<ModelEntry[]>(() => {
    const vendorNames = new Map(
      (pricing?.vendors ?? []).map((vendor) => [vendor.id, vendor.name])
    )
    const metaByModel = new Map(
      (pricing?.data ?? []).map((model) => [
        model.model_name,
        {
          vendor:
            (model.vendor_id ? vendorNames.get(model.vendor_id) : undefined) ??
            '',
          quotaType: model.quota_type,
        },
      ])
    )
    // Keys created earlier can reference models the user can no longer call.
    // Keep them listed so they stay visible and removable.
    const names = [...new Set([...props.models, ...props.value])]
    return names.map((name) => {
      const meta = metaByModel.get(name)
      return { name, vendor: meta?.vendor ?? '', quotaType: meta?.quotaType }
    })
  }, [pricing, props.models, props.value])

  const hasMetadata = entries.some((entry) => entry.quotaType != null)

  // Faceted counts: each filter row counts against the *other* active filters,
  // so a chip never advertises a number that the current view cannot produce.
  const searched = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return entries
    return entries.filter(
      (entry) =>
        entry.name.toLowerCase().includes(query) ||
        entry.vendor.toLowerCase().includes(query)
    )
  }, [entries, search])

  const billingOptions = useMemo<FilterOption[]>(() => {
    const base = searched.filter((entry) => matchesVendor(entry, vendorFilter))
    const countBilling = (filter: string) =>
      base.reduce(
        (count, entry) => count + (matchesBilling(entry, filter) ? 1 : 0),
        0
      )
    return [
      { value: FILTER_ALL, label: t('All'), count: base.length },
      {
        value: BILLING_REQUEST,
        label: t('Per Request'),
        count: countBilling(BILLING_REQUEST),
      },
      {
        value: BILLING_TOKEN,
        label: t('Token-based'),
        count: countBilling(BILLING_TOKEN),
      },
    ]
  }, [searched, vendorFilter, t])

  const vendorOptions = useMemo<FilterOption[]>(() => {
    const base = searched.filter((entry) =>
      matchesBilling(entry, billingFilter)
    )
    const counts = new Map<string, number>()
    let unknown = 0
    for (const entry of base) {
      if (!entry.vendor) {
        unknown += 1
        continue
      }
      counts.set(entry.vendor, (counts.get(entry.vendor) ?? 0) + 1)
    }
    const options = [...counts.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([vendor, count]) => ({ value: vendor, label: vendor, count }))
    if (unknown > 0) {
      options.push({
        value: VENDOR_UNKNOWN,
        label: t('Unknown vendor'),
        count: unknown,
      })
    }
    // The active vendor stays listed even at zero, otherwise the only way back
    // from an empty result would be to guess which chip to press.
    const hasActive = options.some((option) => option.value === vendorFilter)
    if (vendorFilter !== FILTER_ALL && !hasActive) {
      options.push({
        value: vendorFilter,
        label:
          vendorFilter === VENDOR_UNKNOWN ? t('Unknown vendor') : vendorFilter,
        count: 0,
      })
    }
    return [
      { value: FILTER_ALL, label: t('All'), count: base.length },
      ...options,
    ]
  }, [searched, billingFilter, vendorFilter, t])

  const filtered = useMemo(
    () =>
      searched.filter(
        (entry) =>
          matchesBilling(entry, billingFilter) &&
          matchesVendor(entry, vendorFilter)
      ),
    [searched, billingFilter, vendorFilter]
  )

  const selectedSet = useMemo(() => new Set(props.value), [props.value])
  const selectedInFilter = filtered.reduce(
    (count, entry) => count + (selectedSet.has(entry.name) ? 1 : 0),
    0
  )
  const allFilteredSelected =
    filtered.length > 0 && selectedInFilter === filtered.length

  const toggleModel = (name: string, checked: boolean) => {
    if (!checked) {
      props.onChange(props.value.filter((item) => item !== name))
      return
    }
    if (selectedSet.has(name)) return
    props.onChange([...props.value, name])
  }

  const toggleFiltered = (checked: boolean) => {
    if (!checked) {
      const filteredNames = new Set(filtered.map((entry) => entry.name))
      props.onChange(props.value.filter((item) => !filteredNames.has(item)))
      return
    }
    const additions = filtered
      .map((entry) => entry.name)
      .filter((name) => !selectedSet.has(name))
    if (additions.length === 0) return
    props.onChange([...props.value, ...additions])
  }

  return (
    // `min-w-0` is load-bearing: `FormItem` is a grid, and an auto track's
    // minimum is its item's min-content. The list rows carry nowrap model
    // names, so without this the expanded panel widens the whole field and the
    // trigger visibly changes width between collapsed and expanded.
    <Collapsible
      open={expanded}
      onOpenChange={setExpanded}
      disabled={props.disabled}
      className='flex min-w-0 flex-col gap-2'
    >
      <CollapsibleTrigger
        render={
          <button
            type='button'
            id={props.id}
            aria-describedby={props['aria-describedby']}
            aria-invalid={props['aria-invalid']}
            className='border-input bg-background hover:bg-muted/40 focus-visible:border-ring focus-visible:ring-ring/50 flex w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm transition-colors outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50'
          />
        }
      >
        <span
          className={cn(
            'min-w-0 flex-1 truncate',
            props.value.length === 0 && 'text-muted-foreground'
          )}
        >
          {props.value.length === 0
            ? t('Select models (empty for allow all)')
            : t('{{count}} models selected', { count: props.value.length })}
        </span>
        <ChevronDown
          className={cn(
            'text-muted-foreground size-4 shrink-0 transition-transform duration-180 ease-[cubic-bezier(0.32,0.72,0,1)]',
            expanded && 'rotate-180'
          )}
        />
      </CollapsibleTrigger>

      {props.value.length > 0 && (
        <div className='flex items-start gap-2'>
          <div className='flex max-h-24 min-w-0 flex-1 flex-wrap gap-1 overflow-y-auto overscroll-contain'>
            {props.value.slice(0, MAX_VISIBLE_CHIPS).map((name) => (
              <Badge
                key={name}
                variant='secondary'
                className='max-w-full gap-1 pr-1 font-normal'
              >
                <span className='truncate'>{name}</span>
                <button
                  type='button'
                  disabled={props.disabled}
                  onClick={() => toggleModel(name, false)}
                  aria-label={t('Remove')}
                  className='text-muted-foreground hover:text-foreground disabled:pointer-events-none'
                >
                  <X className='size-3' />
                </button>
              </Badge>
            ))}
            {props.value.length > MAX_VISIBLE_CHIPS && (
              <Badge variant='outline'>
                {t('+{{count}} more', {
                  count: props.value.length - MAX_VISIBLE_CHIPS,
                })}
              </Badge>
            )}
          </div>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.disabled}
            onClick={() => props.onChange([])}
            className='h-6 shrink-0 px-2 text-xs'
          >
            {t('Clear all')}
          </Button>
        </div>
      )}

      {/* Reuses the global slideDown/slideUp keyframes but with a snappier
          curve than the shared `.CollapsibleContent` 300ms ease-out, plus a
          fade so the content does not hard-cut at the clipping edge. */}
      <CollapsibleContent
        className={cn(
          'group/panel overflow-hidden',
          'data-open:animate-[slideDown_180ms_cubic-bezier(0.32,0.72,0,1)]',
          'data-closed:animate-[slideUp_150ms_cubic-bezier(0.32,0.72,0,1)]',
          'motion-reduce:animate-none'
        )}
      >
        <div className='flex flex-col gap-3 rounded-lg border p-3 opacity-100 transition-opacity duration-150 group-data-[ending-style]/panel:opacity-0 group-data-[starting-style]/panel:opacity-0 motion-reduce:transition-none'>
          <div className='relative'>
            <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('Search models...')}
              className='pl-8'
            />
          </div>

          {hasMetadata && (
            <div className='flex flex-col gap-2'>
              <FilterRow
                label={t('Billing')}
                options={billingOptions}
                value={billingFilter}
                onChange={setBillingFilter}
              />
              <FilterRow
                label={t('Vendor')}
                options={vendorOptions}
                value={vendorFilter}
                onChange={setVendorFilter}
              />
            </div>
          )}

          <Separator />

          <div className='flex items-center justify-between gap-2'>
            <div className='flex items-center gap-2'>
              <Checkbox
                id={`${uid}-select-all`}
                checked={allFilteredSelected}
                indeterminate={selectedInFilter > 0 && !allFilteredSelected}
                disabled={props.disabled || filtered.length === 0}
                onCheckedChange={(checked) => toggleFiltered(checked === true)}
              />
              <Label
                htmlFor={`${uid}-select-all`}
                className='cursor-pointer text-sm font-normal'
              >
                {t('Select all (filtered)')}
              </Label>
            </div>
            <span className='text-muted-foreground shrink-0 text-xs'>
              {t('{{count}} models', { count: filtered.length })}
            </span>
          </div>

          <div className='-mx-1 max-h-64 overflow-y-auto overscroll-contain px-1'>
            {filtered.length === 0 ? (
              <p className='text-muted-foreground py-6 text-center text-sm'>
                {t('No matching models')}
              </p>
            ) : (
              filtered.map((entry) => (
                <div
                  key={entry.name}
                  className={cn(
                    'hover:bg-muted/50 flex items-center gap-2.5 rounded-md px-2 py-1.5',
                    selectedSet.has(entry.name) && 'bg-muted/40'
                  )}
                >
                  <Checkbox
                    id={`${uid}-${entry.name}`}
                    checked={selectedSet.has(entry.name)}
                    disabled={props.disabled}
                    onCheckedChange={(checked) =>
                      toggleModel(entry.name, checked === true)
                    }
                  />
                  <Label
                    htmlFor={`${uid}-${entry.name}`}
                    className='flex min-w-0 flex-1 cursor-pointer items-center gap-2 font-normal'
                  >
                    <span className='min-w-0 truncate'>{entry.name}</span>
                    {entry.vendor && (
                      <span className='text-muted-foreground shrink-0 text-xs'>
                        {entry.vendor}
                      </span>
                    )}
                  </Label>
                  {entry.quotaType != null && (
                    <Badge
                      variant='secondary'
                      className='shrink-0 font-normal'
                      title={
                        entry.quotaType === QUOTA_TYPE_REQUEST
                          ? t('Per Request')
                          : t('Token-based')
                      }
                    >
                      {entry.quotaType === QUOTA_TYPE_REQUEST
                        ? t('Per Request')
                        : t('Token-based')}
                    </Badge>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
