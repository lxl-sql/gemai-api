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

import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Copy,
  Edit,
  KeyRound,
  Plus,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { deleteOAuthApp, getOAuthApps, resetOAuthAppSecret } from './api'
import { OAuthAppFormDialog } from './components/oauth-app-form-dialog'
import { OAuthAppSecretDialog } from './components/oauth-app-secret-dialog'
import { parseStoredRedirectUris } from './lib'
import type { OAuthApp, OAuthAppSecret } from './types'

async function copyToClipboard(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    return false
  }
}

function formatDate(value: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

export function OAuthAppsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [keyword, setKeyword] = useState('')
  const [submittedKeyword, setSubmittedKeyword] = useState('')
  const [formOpen, setFormOpen] = useState(false)
  const [editingApp, setEditingApp] = useState<OAuthApp | null>(null)
  const [secret, setSecret] = useState<OAuthAppSecret | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const queryKey = useMemo(
    () => ['oauth-apps', submittedKeyword],
    [submittedKeyword]
  )

  const { data, isLoading, isFetching } = useQuery({
    queryKey,
    queryFn: async () => {
      const result = await getOAuthApps(submittedKeyword.trim() || undefined)
      return result.data ?? []
    },
  })

  const apps = data ?? []

  const refreshApps = async () => {
    await queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
  }

  const handleCopy = async (value: string) => {
    const ok = await copyToClipboard(value)
    toast[ok ? 'success' : 'error'](ok ? t('Copied') : t('Copy failed'))
  }

  const handleDelete = async (app: OAuthApp) => {
    if (!window.confirm(t('Delete this OAuth app?'))) return
    setBusyId(app.id)
    try {
      const result = await deleteOAuthApp(app.id)
      if (result.success) {
        toast.success(t('OAuth app deleted'))
        await refreshApps()
      } else {
        toast.error(result.message || t('Failed to delete OAuth app'))
      }
    } finally {
      setBusyId(null)
    }
  }

  const handleResetSecret = async (app: OAuthApp) => {
    const confirmation =
      app.client_type === 'public'
        ? t(
            'Resetting the secret will convert this public OAuth app to a confidential client. Continue?'
          )
        : t('Reset the client secret for this OAuth app?')
    if (!window.confirm(confirmation)) return
    setBusyId(app.id)
    try {
      const result = await resetOAuthAppSecret(app.id)
      if (result.success && result.data?.client_secret) {
        setSecret({
          ...result.data,
          client_id: app.client_id,
          name: app.name,
        })
        toast.success(t('Client secret reset'))
      } else {
        toast.error(result.message || t('Failed to reset client secret'))
      }
    } finally {
      setBusyId(null)
    }
  }

  const handleSaved = async (nextSecret?: OAuthAppSecret) => {
    if (nextSecret?.client_secret) {
      setSecret(nextSecret)
    }
    await refreshApps()
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('OAuth Apps')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            type='button'
            onClick={() => {
              setEditingApp(null)
              setFormOpen(true)
            }}
          >
            <Plus className='size-4' />
            {t('Create OAuth App')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Card className='h-full min-h-0'>
            <CardHeader>
              <CardTitle>{t('OAuth Applications')}</CardTitle>
              <CardDescription>
                {t('Create and manage OAuth clients that can authorize users.')}
              </CardDescription>
            </CardHeader>
            <CardContent className='flex min-h-0 flex-1 flex-col gap-4'>
              <form
                className='flex flex-col gap-2 sm:flex-row'
                onSubmit={(event) => {
                  event.preventDefault()
                  setSubmittedKeyword(keyword)
                }}
              >
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  placeholder={t('Search by application name or client ID...')}
                  className='sm:max-w-sm'
                />
                <div className='flex gap-2'>
                  <Button type='submit' variant='outline'>
                    <Search className='size-4' />
                    {t('Search')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    onClick={() => {
                      setKeyword('')
                      setSubmittedKeyword('')
                    }}
                  >
                    {t('Reset')}
                  </Button>
                </div>
              </form>

              <div className='min-h-0 flex-1 overflow-auto rounded-lg border'>
                <Table>
                  <TableHeader className='bg-muted/30'>
                    <TableRow>
                      <TableHead>{t('Application')}</TableHead>
                      <TableHead>{t('Client ID')}</TableHead>
                      <TableHead>{t('Redirect URIs')}</TableHead>
                      <TableHead>{t('Status')}</TableHead>
                      <TableHead>{t('Created')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Actions')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {isLoading && (
                      <TableRow>
                        <TableCell colSpan={6} className='h-32 text-center'>
                          {t('Loading...')}
                        </TableCell>
                      </TableRow>
                    )}
                    {!isLoading && apps.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={6} className='h-32 text-center'>
                          {t('No OAuth apps found')}
                        </TableCell>
                      </TableRow>
                    )}
                    {!isLoading &&
                      apps.length > 0 &&
                      apps.map((app) => {
                        const redirectUris = parseStoredRedirectUris(
                          app.redirect_uris
                        )
                        const disabled = busyId === app.id || isFetching
                        return (
                          <TableRow key={app.id}>
                            <TableCell className='min-w-[220px]'>
                              <div className='flex items-center gap-3'>
                                {app.logo ? (
                                  <img
                                    src={app.logo}
                                    alt={app.name}
                                    className='ring-border size-9 rounded-lg object-cover ring-1'
                                  />
                                ) : (
                                  <div className='bg-primary/10 text-primary flex size-9 items-center justify-center rounded-lg'>
                                    <KeyRound className='size-4' />
                                  </div>
                                )}
                                <div className='min-w-0'>
                                  <div className='truncate font-medium'>
                                    {app.name}
                                  </div>
                                  {app.description ? (
                                    <div className='text-muted-foreground max-w-[260px] truncate text-xs'>
                                      {app.description}
                                    </div>
                                  ) : null}
                                </div>
                              </div>
                            </TableCell>
                            <TableCell>
                              <div className='flex items-center gap-2 font-mono text-xs'>
                                <span className='max-w-[220px] truncate'>
                                  {app.client_id}
                                </span>
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='icon-sm'
                                  onClick={() => handleCopy(app.client_id)}
                                >
                                  <Copy className='size-4' />
                                  <span className='sr-only'>{t('Copy')}</span>
                                </Button>
                              </div>
                            </TableCell>
                            <TableCell className='min-w-[280px]'>
                              <div className='flex max-w-[420px] flex-col gap-1'>
                                {redirectUris.slice(0, 2).map((uri) => (
                                  <span
                                    key={uri}
                                    className='text-muted-foreground truncate font-mono text-xs'
                                  >
                                    {uri}
                                  </span>
                                ))}
                                {redirectUris.length > 2 ? (
                                  <span className='text-muted-foreground text-xs'>
                                    {t('+{{count}} more', {
                                      count: redirectUris.length - 2,
                                    })}
                                  </span>
                                ) : null}
                              </div>
                            </TableCell>
                            <TableCell>
                              <Badge
                                variant={
                                  app.status === 1 ? 'secondary' : 'outline'
                                }
                              >
                                {app.status === 1
                                  ? t('Enabled')
                                  : t('Disabled')}
                              </Badge>
                            </TableCell>
                            <TableCell className='text-muted-foreground text-xs'>
                              {formatDate(app.created_at)}
                            </TableCell>
                            <TableCell>
                              <div className='flex justify-end gap-1'>
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='sm'
                                  disabled={disabled}
                                  onClick={() => {
                                    setEditingApp(app)
                                    setFormOpen(true)
                                  }}
                                >
                                  <Edit className='size-4' />
                                  {t('Edit')}
                                </Button>
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='sm'
                                  disabled={disabled}
                                  onClick={() => handleResetSecret(app)}
                                >
                                  <RefreshCw className='size-4' />
                                  {t('Reset Secret')}
                                </Button>
                                <Button
                                  type='button'
                                  variant='destructive'
                                  size='sm'
                                  disabled={disabled}
                                  onClick={() => handleDelete(app)}
                                >
                                  <Trash2 className='size-4' />
                                  {t('Delete')}
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                  </TableBody>
                </Table>
              </div>
            </CardContent>
          </Card>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <OAuthAppFormDialog
        open={formOpen}
        app={editingApp}
        onOpenChange={setFormOpen}
        onSaved={handleSaved}
      />
      <OAuthAppSecretDialog
        secret={secret}
        onOpenChange={() => setSecret(null)}
      />
    </>
  )
}
