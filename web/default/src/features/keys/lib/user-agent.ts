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

// Order matters: engines/forks that also carry "Chrome/" or "Safari/" tokens
// must be matched before the generic Chrome/Safari patterns.
const BROWSER_PATTERNS: [RegExp, string][] = [
  [/Edg\/[\d.]+/, 'Edge'],
  [/OPR\/[\d.]+/, 'Opera'],
  [/SamsungBrowser\/[\d.]+/, 'Samsung Internet'],
  [/CriOS\/[\d.]+/, 'Chrome'],
  [/FxiOS\/[\d.]+/, 'Firefox'],
  [/Chrome\/[\d.]+/, 'Chrome'],
  [/Firefox\/[\d.]+/, 'Firefox'],
  [/Version\/[\d.]+.*Safari\//, 'Safari'],
]

// Android UAs also contain "Linux", and iOS UAs also contain "like Mac OS X",
// so the more specific patterns must be checked first.
const OS_PATTERNS: [RegExp, string][] = [
  [/Android/, 'Android'],
  [/iPhone|iPad|iPod/, 'iOS'],
  [/Windows NT/, 'Windows'],
  [/Mac OS X/, 'macOS'],
  [/CrOS/, 'ChromeOS'],
  [/Linux/, 'Linux'],
]

function detectBrowser(userAgent: string): string | null {
  const match = BROWSER_PATTERNS.find(([pattern]) => pattern.test(userAgent))
  return match ? match[1] : null
}

function detectOS(userAgent: string): string | null {
  const match = OS_PATTERNS.find(([pattern]) => pattern.test(userAgent))
  return match ? match[1] : null
}

/**
 * Reduces a raw User-Agent string to a short, human-readable label.
 * Browsers become "Browser · OS"; non-browser HTTP clients (curl,
 * python-requests, SDKs, ...) fall back to their leading "name/version"
 * token, since that is how virtually all of them self-identify.
 */
export function parseClientLabel(userAgent: string): string {
  const trimmed = userAgent.trim()
  if (!trimmed) return ''

  const browser = detectBrowser(trimmed)
  if (browser) {
    const os = detectOS(trimmed)
    return os ? `${browser} · ${os}` : browser
  }

  const clientToken = trimmed.match(/^([A-Za-z][\w.+-]*\/[\w.-]+)/)
  if (clientToken) return clientToken[1]

  return trimmed.split(/\s+/)[0]
}
