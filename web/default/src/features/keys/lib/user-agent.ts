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

// "Name/Version" tokens that describe the rendering engine, browser, or OS
// stack rather than the actual application making the request. Anything
// else in that shape (e.g. "CherryStudio/1.9.12" in an Electron app's UA)
// is the application self-identifying, by the same convention browsers use.
const BOILERPLATE_TOKENS = new Set([
  'mozilla',
  'applewebkit',
  'khtml',
  'gecko',
  'like',
  'chrome',
  'crios',
  'chromium',
  'headlesschrome',
  'firefox',
  'fxios',
  'safari',
  'version',
  'mobile',
  'mobilesafari',
  'edg',
  'edga',
  'edgios',
  'opr',
  'opx',
  'opera',
  'samsungbrowser',
  'electron',
  'cfnetwork',
  'darwin',
  'trident',
  'msie',
])

function detectBrowser(userAgent: string): string | null {
  const match = BROWSER_PATTERNS.find(([pattern]) => pattern.test(userAgent))
  return match ? match[1] : null
}

function detectOS(userAgent: string): string | null {
  const match = OS_PATTERNS.find(([pattern]) => pattern.test(userAgent))
  return match ? match[1] : null
}

function findCustomAppToken(
  userAgent: string
): { name: string; version: string } | null {
  const tokenPattern = /([A-Za-z][\w.+-]*)\/([\w.]+)/g
  for (const match of userAgent.matchAll(tokenPattern)) {
    if (!BOILERPLATE_TOKENS.has(match[1].toLowerCase())) {
      return { name: match[1], version: match[2] }
    }
  }
  return null
}

/**
 * Reduces a raw User-Agent string to a short, human-readable label.
 *
 * Many desktop/Electron clients (chat apps, IDE plugins, ...) embed their
 * own "AppName/Version" token into an otherwise standard browser UA, which
 * identifies the real client far better than the underlying Chromium/Safari
 * engine does - so that takes priority over plain browser detection.
 * Non-browser HTTP clients (curl, python-requests, SDKs, ...) are matched
 * by the same token scan, since that is how virtually all of them
 * self-identify too.
 */
export function parseClientLabel(userAgent: string): string {
  const trimmed = userAgent.trim()
  if (!trimmed) return ''

  const customToken = findCustomAppToken(trimmed)
  if (customToken) {
    const os = detectOS(trimmed)
    return os
      ? `${customToken.name} · ${os}`
      : `${customToken.name}/${customToken.version}`
  }

  const browser = detectBrowser(trimmed)
  if (browser) {
    const os = detectOS(trimmed)
    return os ? `${browser} · ${os}` : browser
  }

  return trimmed.split(/\s+/)[0]
}
