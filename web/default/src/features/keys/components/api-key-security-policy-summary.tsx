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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import type { TokenSecurityPolicyView } from '@/lib/token-security-policy'

function scopeLabel(view: TokenSecurityPolicyView, t: TFunction): string {
  const profile = view.admin_profile
  if (profile.scope_type === 'platform') return t('Platform default')
  if (profile.scope_type === 'group') {
    return `${t('User group')}: ${profile.scope_value}`
  }
  return `${t('User ID')}: ${profile.scope_value}`
}

function displayLimit(value: number, t: TFunction) {
  return value || t('unlimited')
}

export function AdministratorTokenSecuritySummary(props: {
  view: TokenSecurityPolicyView
}) {
  const { t } = useTranslation()
  const profile = props.view.admin_profile
  const builtIn = profile.built_in
  const values = [
    {
      label: t('Sustained requests/second'),
      value: displayLimit(profile.sustained_rps, t),
    },
    {
      label: t('Burst capacity'),
      value: displayLimit(profile.burst_capacity, t),
    },
    {
      label: t('Maximum concurrency'),
      value: displayLimit(profile.max_concurrency, t),
    },
    {
      label: t('Distinct models per 5 minutes'),
      value: displayLimit(profile.max_distinct_models_5m, t),
    },
    {
      label: t('Pause requests when security protection is unavailable'),
      value: profile.fail_closed ? t('Enabled') : t('Disabled'),
    },
  ]

  return (
    <div className='space-y-3'>
      <Alert>
        <AlertTitle className='flex flex-wrap items-center gap-2'>
          {builtIn ? t('Built-in fallback') : t('Administrator policy')}
          <Badge variant='secondary'>
            {builtIn ? t('unlimited') : scopeLabel(props.view, t)}
          </Badge>
        </AlertTitle>
        <AlertDescription>
          {builtIn
            ? t(
                'No administrator traffic policy is configured. Capacity remains unrestricted.'
              )
            : t(
                'Traffic capacity and model-risk thresholds are managed by the administrator. You may only tighten spending limits and risk response.'
              )}
        </AlertDescription>
      </Alert>

      <dl className='grid gap-2 sm:grid-cols-2'>
        {values.map((item) => (
          <div
            key={item.label}
            className='bg-muted/30 rounded-md border px-3 py-2'
          >
            <dt className='text-muted-foreground text-xs'>{item.label}</dt>
            <dd className='mt-1 text-sm font-medium'>{item.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}
