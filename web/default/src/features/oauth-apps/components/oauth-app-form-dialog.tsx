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

import { useEffect, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { createOAuthApp, updateOAuthApp } from '../api'
import {
  getOAuthAppFormError,
  getOAuthAppFormSchema,
  toOAuthAppFormValues,
  toOAuthAppPayload,
} from '../lib'
import type { OAuthApp, OAuthAppFormValues, OAuthAppSecret } from '../types'

interface OAuthAppFormDialogProps {
  open: boolean
  app: OAuthApp | null
  onOpenChange: (open: boolean) => void
  onSaved: (secret?: OAuthAppSecret) => void
}

export function OAuthAppFormDialog(props: OAuthAppFormDialogProps) {
  const { t } = useTranslation()
  const [submitting, setSubmitting] = useState(false)
  const isUpdate = props.app !== null
  const form = useForm<OAuthAppFormValues>({
    resolver: zodResolver(getOAuthAppFormSchema(t)),
    defaultValues: toOAuthAppFormValues(),
  })

  useEffect(() => {
    if (props.open) {
      form.reset(toOAuthAppFormValues(props.app ?? undefined))
    }
  }, [form, props.app, props.open])

  const onSubmit = async (values: OAuthAppFormValues) => {
    const validationError = getOAuthAppFormError(values, t)
    if (validationError) {
      toast.error(validationError)
      return
    }

    setSubmitting(true)
    try {
      const payload = toOAuthAppPayload(values)
      if (isUpdate && props.app) {
        const result = await updateOAuthApp(props.app.id, payload)
        if (result.success) {
          toast.success(t('OAuth app updated'))
          props.onOpenChange(false)
          props.onSaved()
        } else {
          toast.error(result.message || t('Failed to save OAuth app'))
        }
        return
      }

      const result = await createOAuthApp(payload)
      if (result.success) {
        toast.success(t('OAuth app created'))
        props.onOpenChange(false)
        props.onSaved(result.data)
        return
      }
      toast.error(result.message || t('Failed to save OAuth app'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {isUpdate ? t('Edit OAuth App') : t('Create OAuth App')}
          </DialogTitle>
          <DialogDescription>
            {t('Configure an OAuth client that can request user authorization.')}
          </DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form
            id='oauth-app-form'
            className='space-y-4'
            onSubmit={form.handleSubmit(onSubmit)}
          >
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Application name')}</FormLabel>
                  <FormControl>
                    <Input {...field} placeholder={t('Enter application name')} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='description'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Description')}</FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      rows={3}
                      placeholder={t('Describe what this OAuth app is used for')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='logo'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Logo URL')}</FormLabel>
                  <FormControl>
                    <Input {...field} placeholder='https://example.com/logo.png' />
                  </FormControl>
                  <FormDescription>
                    {t('Optional HTTP or HTTPS image URL.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='redirect_uris'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Redirect URIs')}</FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      rows={5}
                      placeholder={'https://example.com/callback\nhttp://localhost:3000/callback'}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Enter one redirect URI per line. HTTPS is required except localhost or loopback HTTP.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </form>
        </Form>
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form='oauth-app-form' disabled={submitting}>
            {submitting ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
