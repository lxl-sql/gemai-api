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

import { z } from 'zod'
import type { TFunction } from 'i18next'
import type { OAuthApp, OAuthAppFormValues, OAuthAppPayload } from './types'

const MAX_REDIRECT_URIS = 20
const MAX_URL_LENGTH = 512

function isLoopbackHost(host: string): boolean {
  const normalized = host.replaceAll(/^\[|\]$/g, '').toLowerCase()
  return (
    normalized === 'localhost' ||
    normalized.endsWith('.localhost') ||
    normalized === '::1' ||
    normalized.startsWith('127.')
  )
}

export function parseRedirectUris(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}

export function parseStoredRedirectUris(value: string): string[] {
  if (!value) return []
  try {
    const parsed = JSON.parse(value) as unknown
    if (Array.isArray(parsed)) {
      return parsed.map((item) => String(item)).filter(Boolean)
    }
  } catch {
    // Legacy data may be stored as a single raw URI string.
  }
  return [value]
}

function validateRedirectUri(uri: string, t: TFunction): string {
  if (!uri) return t('Redirect URI cannot be empty')
  if (uri.length > MAX_URL_LENGTH) return t('Redirect URI is too long')

  let parsed: URL
  try {
    parsed = new URL(uri)
  } catch {
    return t('Invalid redirect URI format')
  }

  if (parsed.hash) return t('Redirect URI must not contain fragment')
  if (parsed.username || parsed.password) {
    return t('Redirect URI must not contain username or password')
  }
  if (parsed.protocol === 'https:') return ''
  if (parsed.protocol === 'http:' && isLoopbackHost(parsed.hostname)) return ''
  return t('Redirect URI must use HTTPS, or HTTP for localhost/loopback')
}

function validateLogoUrl(value: string, t: TFunction): string {
  const logo = value.trim()
  if (!logo) return ''
  if (logo.length > MAX_URL_LENGTH) return t('Logo URL is too long')
  try {
    const parsed = new URL(logo)
    if (!['http:', 'https:'].includes(parsed.protocol)) {
      return t('Logo URL only supports HTTP or HTTPS')
    }
    if (parsed.username || parsed.password) {
      return t('Logo URL must not contain username or password')
    }
  } catch {
    return t('Invalid Logo URL format')
  }
  return ''
}

function validateRedirectUris(value: string, t: TFunction): string {
  const uris = parseRedirectUris(value)
  if (uris.length === 0) return t('Please add at least one redirect URI')
  if (uris.length > MAX_REDIRECT_URIS) {
    return t('Redirect URIs support up to {{count}} entries', {
      count: MAX_REDIRECT_URIS,
    })
  }
  for (const uri of uris) {
    const error = validateRedirectUri(uri, t)
    if (error) return error
  }
  return ''
}

export function getOAuthAppFormSchema(t: TFunction) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, t('Please enter an application name'))
      .max(128, t('Application name is too long')),
    description: z
      .string()
      .max(512, t('Description is too long')),
    logo: z
      .string()
      .superRefine((value, ctx) => {
        const error = validateLogoUrl(value, t)
        if (error) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: error,
          })
        }
      }),
    redirect_uris: z.string().superRefine((value, ctx) => {
      const error = validateRedirectUris(value, t)
      if (error) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          message: error,
        })
      }
    }),
  })
}

export function getOAuthAppFormError(
  values: OAuthAppFormValues,
  t: TFunction
): string {
  return validateLogoUrl(values.logo, t) || validateRedirectUris(values.redirect_uris, t)
}

export function toOAuthAppPayload(values: OAuthAppFormValues): OAuthAppPayload {
  return {
    name: values.name.trim(),
    description: values.description.trim(),
    logo: values.logo.trim(),
    redirect_uris: parseRedirectUris(values.redirect_uris),
  }
}

export function toOAuthAppFormValues(app?: OAuthApp): OAuthAppFormValues {
  return {
    name: app?.name ?? '',
    description: app?.description ?? '',
    logo: app?.logo ?? '',
    redirect_uris: app ? parseStoredRedirectUris(app.redirect_uris).join('\n') : '',
  }
}
