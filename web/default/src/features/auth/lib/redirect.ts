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

const OAUTH_LOGIN_REDIRECT_STORAGE_KEY = 'oauth_login_redirect'

export function normalizeAuthRedirect(
  redirectTo?: string | null
): string | undefined {
  const value = redirectTo?.trim()
  if (!value || value.includes('\\') || value.startsWith('//')) {
    return undefined
  }

  if (typeof window === 'undefined') {
    return value.startsWith('/') ? value : undefined
  }

  try {
    const url = new URL(value, window.location.origin)
    if (url.origin !== window.location.origin) {
      return undefined
    }

    const target = `${url.pathname}${url.search}${url.hash}`
    if (!target.startsWith('/') || target.startsWith('//')) {
      return undefined
    }
    return target
  } catch {
    return undefined
  }
}

export function saveOAuthLoginRedirect(redirectTo?: string | null): void {
  if (typeof window === 'undefined') return

  try {
    const normalized = normalizeAuthRedirect(redirectTo)
    if (normalized) {
      window.sessionStorage.setItem(
        OAUTH_LOGIN_REDIRECT_STORAGE_KEY,
        normalized
      )
    } else {
      window.sessionStorage.removeItem(OAUTH_LOGIN_REDIRECT_STORAGE_KEY)
    }
  } catch {
    /* ignore storage failures */
  }
}

export function consumeOAuthLoginRedirect(): string | undefined {
  if (typeof window === 'undefined') return undefined

  try {
    const redirectTo = window.sessionStorage.getItem(
      OAUTH_LOGIN_REDIRECT_STORAGE_KEY
    )
    window.sessionStorage.removeItem(OAUTH_LOGIN_REDIRECT_STORAGE_KEY)
    return normalizeAuthRedirect(redirectTo)
  } catch {
    return undefined
  }
}
