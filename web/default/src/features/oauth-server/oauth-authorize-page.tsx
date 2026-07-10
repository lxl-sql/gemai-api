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

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import {
  ArrowRight,
  CheckCircle2,
  KeyRound,
  Mail,
  ShieldCheck,
  User,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import { getStatus } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { approveOAuthAuthorization, getOAuthAuthorizationInfo } from './api'
import type { OAuthAuthorizeAppInfo, OAuthAuthorizeParams } from './types'

interface ScopeMeta {
  label: string
  icon: typeof User
}

const SCOPE_META: Record<string, ScopeMeta> = {
  profile: { label: 'Username and profile', icon: User },
  email: { label: 'Email address', icon: Mail },
  'api.token.manage': {
    label: 'Manage your platform API tokens',
    icon: KeyRound,
  },
}

function getCurrentReturnUrl(): string {
  return `${window.location.pathname}${window.location.search}`
}

function EntityAvatar(props: {
  name: string
  logo?: string
  variant: 'app' | 'site'
}) {
  const initial = props.name.trim().charAt(0).toUpperCase() || 'A'
  const variantClass =
    props.variant === 'app'
      ? 'bg-primary text-primary-foreground'
      : 'bg-foreground text-background'

  if (props.logo) {
    return (
      <div className='bg-background flex size-12 items-center justify-center rounded-full p-1 shadow-sm ring-1 ring-border sm:size-14'>
        <img
          src={props.logo}
          alt={props.name}
          className='size-full rounded-full object-cover'
        />
      </div>
    )
  }

  return (
    <div
      className={`${variantClass} flex size-12 items-center justify-center rounded-full text-lg font-bold shadow-sm ring-1 ring-border sm:size-14 sm:text-xl`}
    >
      {initial}
    </div>
  )
}

function buildDenyUrl(
  redirectUri: string,
  state: string | undefined
): string | null {
  try {
    const url = new URL(redirectUri)
    url.searchParams.set('error', 'access_denied')
    url.searchParams.set('error_description', 'user_denied')
    if (state) {
      url.searchParams.set('state', state)
    }
    return url.toString()
  } catch {
    return null
  }
}

export function OAuthAuthorizePage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const resetAuth = useAuthStore((state) => state.auth.reset)
  const [loading, setLoading] = useState(true)
  const [approving, setApproving] = useState(false)
  const [appInfo, setAppInfo] = useState<OAuthAuthorizeAppInfo | null>(null)
  const [error, setError] = useState('')
  const [systemName, setSystemName] = useState('API')
  const [logo, setLogo] = useState('')

  const clientId = String(search.client_id ?? '')
  const redirectUri = String(search.redirect_uri ?? '')
  const scope = String(search.scope ?? 'profile')
  const state = search.state === undefined ? '' : String(search.state)
  const responseType = String(search.response_type ?? 'code')
  const codeChallenge =
    search.code_challenge === undefined ? '' : String(search.code_challenge)
  const codeChallengeMethod =
    search.code_challenge_method === undefined
      ? ''
      : String(search.code_challenge_method)

  const authorizeParams = useMemo<OAuthAuthorizeParams>(
    () => ({
      client_id: clientId,
      redirect_uri: redirectUri,
      scope,
      ...(codeChallenge
        ? {
            code_challenge: codeChallenge,
            code_challenge_method: codeChallengeMethod || 'S256',
          }
        : {}),
    }),
    [clientId, codeChallenge, codeChallengeMethod, redirectUri, scope]
  )

  const redirectToLogin = useCallback(() => {
    resetAuth()
    navigate({
      to: '/sign-in',
      search: {
        redirect: getCurrentReturnUrl(),
      },
      replace: true,
    })
  }, [navigate, resetAuth])

  const fetchAppInfo = useCallback(async () => {
    if (!clientId || !redirectUri) {
      setError(t('Missing required parameters: client_id or redirect_uri'))
      setLoading(false)
      return
    }

    if (responseType !== 'code') {
      setError(t('Only response_type=code is supported'))
      setLoading(false)
      return
    }

    let shouldStopLoading = true
    try {
      const result = await getOAuthAuthorizationInfo(authorizeParams)
      if (!result.success) {
        setError(result.message || t('Unable to load application information'))
        return
      }

      if (!result.data?.logged_in) {
        shouldStopLoading = false
        redirectToLogin()
        return
      }

      setAppInfo(result.data)
    } catch (err: unknown) {
      const status = (err as { response?: { status?: number } })?.response?.status
      if (status === 401) {
        shouldStopLoading = false
        redirectToLogin()
        return
      }
      setError(t('Unable to load application information'))
    } finally {
      if (shouldStopLoading) {
        setLoading(false)
      }
    }
  }, [authorizeParams, clientId, redirectToLogin, redirectUri, responseType, t])

  useEffect(() => {
    getStatus()
      .then((status) => {
        const statusName = status?.system_name
        const statusLogo = status?.logo
        if (typeof statusName === 'string' && statusName.trim()) {
          setSystemName(statusName)
        }
        if (typeof statusLogo === 'string') {
          setLogo(statusLogo)
        }
      })
      .catch(() => {
        /* status is decorative on this page */
      })
  }, [])

  useEffect(() => {
    void fetchAppInfo()

    const handlePageShow = (event: PageTransitionEvent) => {
      if (event.persisted) {
        setApproving(false)
        void fetchAppInfo()
      }
    }

    window.addEventListener('pageshow', handlePageShow)
    return () => window.removeEventListener('pageshow', handlePageShow)
  }, [fetchAppInfo])

  const handleApprove = async () => {
    if (!appInfo) return

    setApproving(true)
    try {
      const result = await approveOAuthAuthorization({
        client_id: clientId,
        redirect_uri: redirectUri,
        scope: appInfo.scope || scope,
        state,
        csrf_token: appInfo.csrf_token || '',
      })

      if (!result.success || !result.data?.redirect_url) {
        toast.error(result.message || t('Authorization failed'))
        setApproving(false)
        return
      }

      window.location.href = result.data.redirect_url
    } catch {
      toast.error(t('Authorization failed'))
      setApproving(false)
    }
  }

  const handleDeny = () => {
    const denyUrl = buildDenyUrl(appInfo?.redirect_uri || redirectUri, state)
    if (!denyUrl) {
      toast.error(t('Invalid callback URL'))
      return
    }
    window.location.href = denyUrl
  }

  const scopeList = (appInfo?.scope || scope).split(' ').filter(Boolean)
  const appName = appInfo?.app_name || appInfo?.name || t('OAuth application')
  const userName =
    appInfo?.user?.display_name ||
    appInfo?.user?.username ||
    appInfo?.username ||
    ''

  return (
    <div className='from-background to-muted/40 relative flex min-h-screen items-center justify-center overflow-hidden bg-gradient-to-b px-4 py-6'>
      <div className='bg-primary/10 absolute -top-24 -right-20 size-72 rounded-full blur-3xl' />
      <div className='bg-cyan-500/10 absolute top-1/2 -left-28 size-80 rounded-full blur-3xl' />

      <div className='relative w-full max-w-[460px] space-y-5'>
        <div className='flex items-center justify-center gap-3'>
          {logo ? (
            <img
              src={logo}
              alt={t('Logo')}
              className='size-10 rounded-full object-cover ring-1 ring-border'
            />
          ) : null}
          <div className='text-xl font-bold tracking-tight'>{systemName}</div>
        </div>

        <Card className='relative mx-auto overflow-visible rounded-3xl border-border/70 bg-card/95 py-0 shadow-2xl shadow-foreground/5 backdrop-blur'>
          {loading && (
            <CardContent className='flex min-h-[360px] items-center justify-center'>
              <Spinner />
            </CardContent>
          )}
          {!loading && error && (
            <>
              <CardHeader className='items-center px-7 pt-8 text-center'>
                <div className='bg-destructive/10 text-destructive mb-3 flex size-16 items-center justify-center rounded-full'>
                  <XCircle className='size-7' />
                </div>
                <CardTitle className='text-2xl font-bold'>
                  {t('Authorization failed')}
                </CardTitle>
                <CardDescription>{error}</CardDescription>
              </CardHeader>
              <CardContent className='px-7 pb-7'>
                <Button
                  className='h-11 w-full rounded-full'
                  onClick={() => navigate({ to: '/' })}
                >
                  {t('Return home')}
                </Button>
              </CardContent>
            </>
          )}
          {!loading && !error && appInfo && (
            <>
              <div className='absolute top-0 left-1/2 z-10 -translate-x-1/2 -translate-y-1/2 rounded-full bg-background px-3'>
                <div className='bg-card text-foreground flex size-10 items-center justify-center rounded-full shadow-md ring-1 ring-border'>
                  <ShieldCheck className='size-5' />
                </div>
              </div>

              <CardHeader className='justify-items-center px-5 pt-10 pb-0 text-center sm:px-6'>
                <div className='space-y-1'>
                  <CardTitle className='text-xl font-bold tracking-tight'>
                    {t('Authorize OAuth App')}
                  </CardTitle>
                  <CardDescription className='mx-auto max-w-[340px] text-xs sm:text-sm'>
                    {t('{{app}} wants to access your {{site}} account.', {
                      app: appName,
                      site: systemName,
                    })}
                  </CardDescription>
                </div>
              </CardHeader>

              <CardContent className='space-y-4 px-5 pt-5 pb-6 sm:px-6'>
                <div className='bg-muted/30 rounded-2xl border border-border/60 p-3.5 sm:p-4'>
                  <div className='flex items-center justify-center gap-3 sm:gap-4'>
                    <EntityAvatar
                      name={appName}
                      logo={appInfo.logo}
                      variant='app'
                    />
                    <div className='text-muted-foreground flex shrink-0 items-center gap-1.5 sm:gap-2'>
                      <span className='size-1 rounded-full bg-current opacity-30' />
                      <span className='size-1.5 rounded-full bg-current opacity-50' />
                      <div className='bg-background flex size-6 items-center justify-center rounded-full ring-1 ring-border'>
                        <ArrowRight className='size-3.5 opacity-70' />
                      </div>
                      <span className='size-1.5 rounded-full bg-current opacity-50' />
                      <span className='size-1 rounded-full bg-current opacity-30' />
                    </div>
                    <EntityAvatar name={systemName} logo={logo} variant='site' />
                  </div>

                  <div className='mt-3 max-w-full text-center'>
                    <div className='text-base font-semibold'>{appName}</div>
                    {appInfo.description ? (
                      <p className='text-muted-foreground mx-auto mt-1 max-w-[360px] text-sm'>
                        {appInfo.description}
                      </p>
                    ) : null}
                    {userName ? (
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {t('Signed in as {{username}}', { username: userName })}
                      </p>
                    ) : null}
                  </div>
                </div>

                <div className='rounded-2xl border border-border/70 bg-background/70 p-3.5'>
                  <div className='text-muted-foreground mb-3 text-sm font-medium'>
                    {t('Requested permissions')}
                  </div>
                  <div className='space-y-2'>
                    {scopeList.map((item) => {
                      const meta = SCOPE_META[item]
                      const Icon = meta?.icon || KeyRound
                      return (
                        <div
                          key={item}
                          className='bg-muted/40 flex items-center gap-2.5 rounded-xl border border-border/60 px-3 py-2.5 text-sm'
                        >
                          <CheckCircle2 className='size-4 shrink-0 text-emerald-500' />
                          <span className='flex min-w-0 items-center gap-2'>
                            <Icon className='text-muted-foreground size-4 shrink-0' />
                            <span className='truncate'>
                              {t(meta?.label || item)}
                            </span>
                          </span>
                        </div>
                      )
                    })}
                  </div>
                </div>

                <p className='text-muted-foreground px-1 text-sm leading-relaxed'>
                  {t(
                    'After authorization, {{app}} will be able to read information within the selected scope. You can revoke this authorization at any time.',
                    { app: appName }
                  )}
                </p>

                <div className='space-y-2.5'>
                  <Button
                    type='button'
                    onClick={handleApprove}
                    disabled={approving}
                    className='h-11 w-full rounded-full text-sm font-semibold'
                  >
                    {approving ? t('Authorizing...') : t('Authorize')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    onClick={handleDeny}
                    disabled={approving}
                    className='h-11 w-full rounded-full text-sm'
                  >
                    {t('Cancel')}
                  </Button>
                </div>
              </CardContent>
            </>
          )}
        </Card>
      </div>
    </div>
  )
}
