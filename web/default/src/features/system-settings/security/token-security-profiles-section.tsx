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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { Pencil, Plus, RotateCcw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Form,
  FormControl,
  FormDescription,
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { getGroups } from '@/features/users/api'
import type { TokenSecurityProfile } from '@/lib/token-security-policy'
import {
  EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  tokenSecurityProfileToValues,
  tokenSecurityProfileValuesSchema,
  type TokenSecurityProfileValues,
} from '@/lib/token-security-profile-form'

import {
  deleteTokenSecurityProfile,
  getTokenSecurityProfiles,
  updateTokenSecurityProfile,
} from '../api'
import { SettingsSection } from '../components/settings-section'
import { TokenSecurityProfileFields } from './token-security-profile-fields'

type ProfileScope = TokenSecurityProfile['scope_type']

function scopeLabel(profile: TokenSecurityProfile, t: TFunction): string {
  if (profile.scope_type === 'platform') return t('Platform default')
  if (profile.scope_type === 'group') {
    return `${t('User group')}: ${profile.scope_value}`
  }
  return `${t('User ID')}: ${profile.scope_value}`
}

function riskModeLabel(
  mode: TokenSecurityProfile['minimum_risk_mode'],
  t: TFunction
): string {
  if (mode === 'suspend') return t('Automatically suspend token')
  if (mode === 'notify') return t('Audit and notify')
  return t('Audit only')
}

function scopeTargetLabel(scopeType: ProfileScope, t: TFunction): string {
  if (scopeType === 'group') return t('User group name')
  if (scopeType === 'user') return t('User ID')
  return t('Policy target')
}

export function TokenSecurityProfilesSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const verification = useSecureVerification()
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingProfile, setEditingProfile] =
    useState<TokenSecurityProfile | null>(null)
  const [scopeType, setScopeType] = useState<ProfileScope>('group')
  const [scopeValue, setScopeValue] = useState('')
  const [scopeError, setScopeError] = useState('')
  const [profileToDelete, setProfileToDelete] =
    useState<TokenSecurityProfile | null>(null)

  const profilesQuery = useQuery({
    queryKey: ['token-security-profiles'],
    queryFn: async () => {
      const result = await getTokenSecurityProfiles()
      if (!result.success) {
        throw new Error(result.message || t('Failed to load security profiles'))
      }
      return result.data ?? []
    },
  })
  const groupsQuery = useQuery({
    queryKey: ['admin-groups'],
    queryFn: async () => {
      const result = await getGroups()
      if (!result.success) {
        throw new Error(result.message || t('Failed to load security profiles'))
      }
      return result.data ?? []
    },
  })
  const groups = groupsQuery.data ?? []
  const profiles = profilesQuery.data ?? []
  const scopeItems = [
    { value: 'group', label: t('User group') },
    { value: 'user', label: t('User') },
  ]
  const editorScopeItems =
    editingProfile?.scope_type === 'platform'
      ? [{ value: 'platform', label: t('Platform default') }]
      : scopeItems
  const configuredGroups = new Set(
    profiles
      .filter((profile) => profile.scope_type === 'group')
      .map((profile) => profile.scope_value)
  )
  const groupItems = groups
    .filter(
      (group) =>
        editingProfile?.scope_value === group || !configuredGroups.has(group)
    )
    .map((group) => ({ value: group, label: group }))

  const form = useForm<TokenSecurityProfileValues>({
    resolver: zodResolver(tokenSecurityProfileValuesSchema),
    mode: 'onChange',
    defaultValues: EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  })

  const closeEditor = () => {
    setEditorOpen(false)
    setEditingProfile(null)
    setScopeError('')
  }

  const openNewEditor = () => {
    setEditingProfile(null)
    setScopeType(groupItems.length > 0 ? 'group' : 'user')
    setScopeValue('')
    setScopeError('')
    form.reset(EMPTY_TOKEN_SECURITY_PROFILE_VALUES)
    setEditorOpen(true)
  }

  const openEditEditor = (profile: TokenSecurityProfile) => {
    setEditingProfile(profile)
    setScopeType(profile.scope_type)
    setScopeValue(profile.scope_value)
    setScopeError('')
    form.reset(tokenSecurityProfileToValues(profile))
    setEditorOpen(true)
  }

  const saveMutation = useMutation({
    mutationFn: async (values: TokenSecurityProfileValues) => {
      const normalizedScopeValue =
        scopeType === 'platform' ? '' : scopeValue.trim()
      if (!normalizedScopeValue && scopeType !== 'platform') {
        throw new Error(t('Scope value is required'))
      }
      if (
        scopeType === 'user' &&
        (!/^\d+$/.test(normalizedScopeValue) ||
          Number.parseInt(normalizedScopeValue, 10) <= 0)
      ) {
        throw new Error(t('Enter a valid user ID'))
      }
      if (
        editingProfile === null &&
        profiles.some(
          (profile) =>
            profile.scope_type === scopeType &&
            profile.scope_value === normalizedScopeValue
        )
      ) {
        throw new Error(
          t(
            'A security profile already exists for this target. Edit the existing profile instead.'
          )
        )
      }
      const result = await updateTokenSecurityProfile(
        {
          ...values,
          scope_type: scopeType,
          scope_value: normalizedScopeValue,
        },
        editingProfile === null
      )
      if (!result.success) {
        throw new Error(result.message || t('Failed to update setting'))
      }
      return result
    },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({
        queryKey: ['token-security-profiles'],
      })
      if (!result.data.cache_synchronized) {
        toast.warning(
          t(
            'The security profile changed, but cache synchronization is still pending.'
          )
        )
      } else {
        toast.success(t('Token security profile saved'))
      }
      closeEditor()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async (profile: TokenSecurityProfile) => {
      const result = await deleteTokenSecurityProfile(
        profile.scope_type,
        profile.scope_value
      )
      if (!result.success) {
        throw new Error(result.message || t('Failed to update setting'))
      }
      return result
    },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({
        queryKey: ['token-security-profiles'],
      })
      if (!result.data.cache_synchronized) {
        toast.warning(
          t(
            'The security profile changed, but cache synchronization is still pending.'
          )
        )
      } else {
        toast.success(t('Token security profile reset'))
      }
      setProfileToDelete(null)
    },
  })

  const onSubmit = async (values: TokenSecurityProfileValues) => {
    if (scopeType !== 'platform' && !scopeValue.trim()) {
      setScopeError(t('Scope value is required'))
      return
    }
    setScopeError('')
    try {
      await verification.withVerification(
        () => saveMutation.mutateAsync(values),
        {
          title: t('Security verification'),
          description: t(
            'Confirm your identity before changing API Key security profiles.'
          ),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to update setting')
      )
    }
  }

  const confirmDelete = async () => {
    if (!profileToDelete) return
    const profile = profileToDelete
    setProfileToDelete(null)
    try {
      await verification.withVerification(
        () => deleteMutation.mutateAsync(profile),
        {
          title: t('Security verification'),
          description: t(
            'Confirm your identity before changing API Key security profiles.'
          ),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to update setting')
      )
    }
  }

  return (
    <SettingsSection title={t('API Key Security Profiles')}>
      <Alert>
        <AlertTitle>{t('Administrator-enforced boundaries')}</AlertTitle>
        <AlertDescription>
          {t(
            'The most specific profile wins: user, then user group, then platform. API key owners may only choose stricter limits. A value of 0 means no administrator maximum.'
          )}
        </AlertDescription>
      </Alert>

      {(profilesQuery.isError || groupsQuery.isError) && (
        <Alert variant='destructive'>
          <AlertTitle>{t('Failed to load security profiles')}</AlertTitle>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
            <span>{t('Check the connection and try again.')}</span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => {
                void Promise.all([
                  profilesQuery.refetch(),
                  groupsQuery.refetch(),
                ])
              }}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      <div className='flex justify-end'>
        <Button type='button' variant='outline' onClick={openNewEditor}>
          <Plus className='size-4' aria-hidden='true' />
          {t('New profile')}
        </Button>
      </div>

      <div className='overflow-hidden rounded-xl border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Policy scope')}</TableHead>
              <TableHead>{t('Traffic limits')}</TableHead>
              <TableHead>{t('Minimum risk response')}</TableHead>
              <TableHead className='w-24 text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {profiles.map((profile) => (
              <TableRow key={`${profile.scope_type}:${profile.scope_value}`}>
                <TableCell>
                  <div className='flex items-center gap-2'>
                    <span className='font-medium'>
                      {scopeLabel(profile, t)}
                    </span>
                    {profile.built_in && (
                      <Badge variant='secondary'>
                        {t('Built-in fallback')}
                      </Badge>
                    )}
                  </div>
                </TableCell>
                <TableCell className='text-muted-foreground'>
                  {t(
                    '{{rps}} RPS, burst {{burst}}, concurrency {{concurrency}}, {{models}} models per 5 minutes',
                    {
                      rps: profile.sustained_rps || t('unlimited'),
                      burst: profile.burst_capacity || t('unlimited'),
                      concurrency: profile.max_concurrency || t('unlimited'),
                      models: profile.max_distinct_models_5m || t('unlimited'),
                    }
                  )}
                </TableCell>
                <TableCell>
                  {riskModeLabel(profile.minimum_risk_mode, t)}
                </TableCell>
                <TableCell>
                  <div className='flex justify-end gap-1'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('Edit')}
                      onClick={() => openEditEditor(profile)}
                    >
                      <Pencil className='size-4' aria-hidden='true' />
                    </Button>
                    {!profile.built_in && (
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={t('Reset')}
                        disabled={deleteMutation.isPending}
                        onClick={() => setProfileToDelete(profile)}
                      >
                        {profile.scope_type === 'platform' ? (
                          <RotateCcw className='size-4' aria-hidden='true' />
                        ) : (
                          <Trash2 className='size-4' aria-hidden='true' />
                        )}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog
        open={editorOpen}
        onOpenChange={(open) => {
          if (!open) closeEditor()
        }}
      >
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>
              {editingProfile
                ? t('Edit security profile')
                : t('New security profile')}
            </DialogTitle>
            <DialogDescription>
              {editingProfile
                ? t(
                    'The policy scope is locked while editing to prevent leaving an unintended duplicate.'
                  )
                : t(
                    'Create a policy for a user group or a specific user. Edit the platform default from its existing row.'
                  )}
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form
              className='space-y-6'
              onSubmit={form.handleSubmit(onSubmit)}
              noValidate
            >
              <div className='grid gap-5 sm:grid-cols-2'>
                <FormItem>
                  <FormLabel>{t('Policy scope')}</FormLabel>
                  <Select
                    items={editorScopeItems}
                    value={scopeType}
                    onValueChange={(value) => {
                      if (!value) return
                      setScopeType(value as ProfileScope)
                      setScopeValue('')
                      setScopeError('')
                    }}
                    disabled={editingProfile !== null}
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectGroup>
                        {editingProfile?.scope_type === 'platform' ? (
                          <SelectItem value='platform'>
                            {t('Platform default')}
                          </SelectItem>
                        ) : (
                          editorScopeItems.map((item) => (
                            <SelectItem key={item.value} value={item.value}>
                              {item.label}
                            </SelectItem>
                          ))
                        )}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'User overrides replace group profiles, and group profiles replace the platform profile.'
                    )}
                  </FormDescription>
                </FormItem>

                <FormItem>
                  <FormLabel>{scopeTargetLabel(scopeType, t)}</FormLabel>
                  {scopeType === 'group' ? (
                    <Select
                      items={groupItems}
                      value={scopeValue}
                      onValueChange={(value) => {
                        setScopeValue(value ?? '')
                        setScopeError('')
                      }}
                      disabled={
                        editingProfile !== null || groupsQuery.isPending
                      }
                    >
                      <FormControl>
                        <SelectTrigger
                          className='w-full'
                          aria-invalid={scopeError ? true : undefined}
                        >
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectGroup>
                          {groupItems.map((item) => (
                            <SelectItem key={item.value} value={item.value}>
                              {item.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  ) : (
                    <FormControl>
                      <Input
                        value={scopeValue}
                        onChange={(event) => {
                          setScopeValue(event.target.value)
                          setScopeError('')
                        }}
                        disabled={
                          editingProfile !== null || scopeType === 'platform'
                        }
                        inputMode='numeric'
                        aria-invalid={scopeError ? true : undefined}
                        placeholder={
                          scopeType === 'platform'
                            ? t('Not required for platform scope')
                            : t('Enter a numeric user ID')
                        }
                      />
                    </FormControl>
                  )}
                  <FormDescription>
                    {scopeType === 'platform'
                      ? t('Applies when no group or user override exists.')
                      : t('The value must match an existing account scope.')}
                  </FormDescription>
                  {scopeError && <FormMessage>{scopeError}</FormMessage>}
                </FormItem>
              </div>

              <TokenSecurityProfileFields form={form} />

              <DialogFooter>
                <Button type='button' variant='outline' onClick={closeEditor}>
                  {t('Cancel')}
                </Button>
                <Button
                  type='submit'
                  disabled={!form.formState.isValid || saveMutation.isPending}
                >
                  {t('Save security profile')}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={profileToDelete !== null}
        onOpenChange={(open) => {
          if (!open) setProfileToDelete(null)
        }}
        title={t('Reset security profile?')}
        desc={t(
          'This removes the administrator override. Matching API keys will immediately inherit the next applicable profile.'
        )}
        confirmText={t('Reset profile')}
        destructive
        isLoading={deleteMutation.isPending}
        handleConfirm={() => void confirmDelete()}
      />

      <SecureVerificationDialog
        open={verification.open}
        onOpenChange={(open) => {
          if (!open) verification.cancel()
        }}
        methods={verification.methods}
        state={verification.state}
        onVerify={async (method, code) => {
          await verification.executeVerification(method, code)
        }}
        onCancel={verification.cancel}
        onCodeChange={verification.setCode}
        onMethodChange={verification.switchMethod}
      />
    </SettingsSection>
  )
}
