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
import { AlertCircle, Gauge } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { getDefaultTokenSecurityPolicy } from '@/features/keys/api'
import { AdministratorTokenSecuritySummary } from '@/features/keys/components/api-key-security-policy-summary'

export function AccountLimitsCard() {
  const { t } = useTranslation()

  const query = useQuery({
    queryKey: ['account-token-security-policy'],
    staleTime: 60_000,
    queryFn: async () => {
      const response = await getDefaultTokenSecurityPolicy()
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load account limits'))
      }
      return response.data
    },
  })

  if (query.isLoading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='p-3 sm:p-5'>
          <Skeleton className='h-6 w-48' />
          <Skeleton className='mt-2 h-4 w-64' />
        </CardHeader>
        <CardContent className='space-y-2 p-3 sm:p-5'>
          <Skeleton className='h-16 w-full' />
          <div className='grid gap-2 sm:grid-cols-2'>
            <Skeleton className='h-14 w-full' />
            <Skeleton className='h-14 w-full' />
            <Skeleton className='h-14 w-full' />
            <Skeleton className='h-14 w-full' />
          </div>
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title={t('Account and API key limits')}
      description={t(
        'Includes limits applied separately to each API key and limits shared by all API keys owned by this account.'
      )}
      icon={<Gauge className='h-4 w-4' />}
      disableHoverEffect
    >
      {query.isError || !query.data ? (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 text-destructive flex items-center gap-2 rounded-lg border p-4 text-sm'
        >
          <AlertCircle className='size-4 shrink-0' aria-hidden='true' />
          <span>
            {query.error instanceof Error
              ? query.error.message
              : t('Failed to load account limits')}
          </span>
        </div>
      ) : (
        <AdministratorTokenSecuritySummary view={query.data} />
      )}
    </TitledCard>
  )
}
