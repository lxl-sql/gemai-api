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
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Form } from '@/components/ui/form'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import {
  deleteTokenSecurityProfile,
  getTokenSecurityProfiles,
  updateTokenSecurityProfile,
} from '@/features/system-settings/api'
import { TokenSecurityProfileFields } from '@/features/system-settings/security/token-security-profile-fields'
import {
  UNRESTRICTED_TOKEN_SECURITY_PROFILE,
  type TokenSecurityProfile,
} from '@/lib/token-security-policy'
import {
  DEFAULT_TOKEN_SECURITY_PROFILE_VALUES,
  EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  tokenSecurityProfileToValues,
  tokenSecurityProfileValuesToPayload,
  tokenSecurityProfileValuesSchema,
  type TokenSecurityProfileValues,
} from '@/lib/token-security-profile-form'

import type { User } from '../../types'

type PolicyMode = 'inherit' | 'custom'

function inheritedProfile(
  profiles: TokenSecurityProfile[],
  userGroup: string
): TokenSecurityProfile {
  return (
    profiles.find(
      (profile) =>
        profile.scope_type === 'group' && profile.scope_value === userGroup
    ) ??
    profiles.find((profile) => profile.scope_type === 'platform') ?? {
      ...UNRESTRICTED_TOKEN_SECURITY_PROFILE,
      id: 0,
    }
  )
}

export function UserTokenSecurityProfileDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const verification = useSecureVerification()
  const [mode, setMode] = useState<PolicyMode>('inherit')
  const form = useForm<TokenSecurityProfileValues>({
    resolver: zodResolver(tokenSecurityProfileValuesSchema),
    mode: 'onChange',
    defaultValues: EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  })

  const profilesQuery = useQuery({
    queryKey: ['token-security-profiles'],
    enabled: props.open,
    queryFn: async () => {
      const result = await getTokenSecurityProfiles()
      if (!result.success) {
        throw new Error(result.message || t('Failed to load security profiles'))
      }
      return result.data ?? []
    },
  })
  const modeItems = [
    { value: 'inherit', label: t('Inherit default policy') },
    { value: 'custom', label: t('Custom user policy') },
  ]

  useEffect(() => {
    const loadedProfiles = profilesQuery.data
    if (!props.open || !loadedProfiles) return
    const loadedDirectProfile = loadedProfiles.find(
      (profile) =>
        profile.scope_type === 'user' &&
        profile.scope_value === String(props.user.id)
    )
    const loadedInheritedProfile = inheritedProfile(
      loadedProfiles,
      props.user.group
    )
    const inheritedValues = loadedInheritedProfile.built_in
      ? DEFAULT_TOKEN_SECURITY_PROFILE_VALUES
      : tokenSecurityProfileToValues(loadedInheritedProfile)
    setMode(loadedDirectProfile ? 'custom' : 'inherit')
    form.reset(
      loadedDirectProfile
        ? tokenSecurityProfileToValues(loadedDirectProfile)
        : inheritedValues
    )
  }, [form, profilesQuery.data, props.open, props.user.group, props.user.id])

  const closeDialog = () => {
    verification.cancel()
    props.onOpenChange(false)
  }

  const saveMutation = useMutation({
    mutationFn: async (values: TokenSecurityProfileValues) => {
      if (mode === 'inherit') {
        const result = await deleteTokenSecurityProfile(
          'user',
          String(props.user.id)
        )
        if (!result.success) {
          throw new Error(result.message || t('Failed to update setting'))
        }
        return {
          cacheSynchronized: result.data.cache_synchronized,
        }
      }

      const result = await updateTokenSecurityProfile({
        ...tokenSecurityProfileValuesToPayload(values),
        scope_type: 'user',
        scope_value: String(props.user.id),
      })
      if (!result.success) {
        throw new Error(result.message || t('Failed to update setting'))
      }
      return {
        cacheSynchronized: result.data.cache_synchronized,
      }
    },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({
        queryKey: ['token-security-profiles'],
      })
      if (!result.cacheSynchronized) {
        toast.warning(
          t(
            'The security profile changed, but cache synchronization is still pending.'
          )
        )
      } else {
        toast.success(t('Token security profile saved'))
      }
      closeDialog()
    },
  })

  const onSubmit = async (values: TokenSecurityProfileValues) => {
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

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
      >
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>{t('API Key security policy')}</DialogTitle>
            <DialogDescription>
              {t('Configure the API Key policy for {{username}}.', {
                username: props.user.username,
              })}
            </DialogDescription>
          </DialogHeader>

          {profilesQuery.isError ? (
            <Alert variant='destructive'>
              <AlertTitle>{t('Failed to load security profiles')}</AlertTitle>
              <AlertDescription>
                {t('Check the connection and try again.')}
              </AlertDescription>
            </Alert>
          ) : (
            <Form {...form}>
              <form
                className='space-y-6'
                onSubmit={form.handleSubmit(onSubmit)}
                noValidate
              >
                <div className='space-y-2'>
                  <Label htmlFor='user-token-security-policy-mode'>
                    {t('Policy mode')}
                  </Label>
                  <Select
                    items={modeItems}
                    value={mode}
                    onValueChange={(value) => {
                      if (value) setMode(value as PolicyMode)
                    }}
                    disabled={profilesQuery.isPending}
                  >
                    <SelectTrigger
                      id='user-token-security-policy-mode'
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {modeItems.map((item) => (
                          <SelectItem key={item.value} value={item.value}>
                            {item.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <p className='text-muted-foreground text-xs'>
                    {mode === 'inherit'
                      ? t(
                          'The user inherits the user group policy first, then the platform policy.'
                        )
                      : t(
                          'This policy applies to each API Key owned by this user.'
                        )}
                  </p>
                </div>

                {mode === 'custom' && (
                  <TokenSecurityProfileFields form={form} />
                )}

                <DialogFooter>
                  <Button type='button' variant='outline' onClick={closeDialog}>
                    {t('Cancel')}
                  </Button>
                  <Button
                    type='submit'
                    disabled={
                      profilesQuery.isPending ||
                      saveMutation.isPending ||
                      (mode === 'custom' && !form.formState.isValid)
                    }
                  >
                    {t('Save security profile')}
                  </Button>
                </DialogFooter>
              </form>
            </Form>
          )}
        </DialogContent>
      </Dialog>

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
    </>
  )
}
