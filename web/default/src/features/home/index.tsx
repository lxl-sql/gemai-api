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
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { RichContent } from '@/components/rich-content'
import { useTheme } from '@/context/theme-provider'
import { isLikelyHtml } from '@/lib/content-format'
import { useAuthStore } from '@/stores/auth-store'

import { Hero } from './components/sections/hero'
import { useHomePageContent } from './hooks'

const Stats = lazy(() =>
  import('./components/sections/stats').then((module) => ({
    default: module.Stats,
  }))
)
const Features = lazy(() =>
  import('./components/sections/features').then((module) => ({
    default: module.Features,
  }))
)
const HowItWorks = lazy(() =>
  import('./components/sections/how-it-works').then((module) => ({
    default: module.HowItWorks,
  }))
)
const CTA = lazy(() =>
  import('./components/sections/cta').then((module) => ({
    default: module.CTA,
  }))
)
const Footer = lazy(() =>
  import('@/components/layout/components/footer').then((module) => ({
    default: module.Footer,
  }))
)

export function Home() {
  const { i18n, t } = useTranslation()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const { resolvedTheme } = useTheme()
  const { auth } = useAuthStore()
  const isAuthenticated = !!auth.user
  const { content, isUrl } = useHomePageContent()
  const [showDeferredSections, setShowDeferredSections] = useState(false)

  useEffect(() => {
    if (typeof window.requestIdleCallback === 'function') {
      const idleId = window.requestIdleCallback(
        () => setShowDeferredSections(true),
        { timeout: 800 }
      )
      return () => window.cancelIdleCallback(idleId)
    }

    const timeoutId = window.setTimeout(
      () => setShowDeferredSections(true),
      200
    )
    return () => window.clearTimeout(timeoutId)
  }, [])

  const syncIframePreferences = useCallback(() => {
    try {
      iframeRef.current?.contentWindow?.postMessage(
        { themeMode: resolvedTheme },
        '*'
      )
      iframeRef.current?.contentWindow?.postMessage(
        { lang: i18n.language },
        '*'
      )
    } catch {
      // Cross-origin frames may reject access while navigating.
    }
  }, [i18n.language, resolvedTheme])

  useEffect(() => {
    if (isUrl) {
      syncIframePreferences()
    }
  }, [isUrl, syncIframePreferences])

  if (content) {
    if (isUrl) {
      return (
        <PublicLayout showMainContainer={false}>
          <iframe
            ref={iframeRef}
            src={content}
            className='h-screen w-full border-none'
            title={t('Custom Home Page')}
            sandbox='allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts'
            onLoad={syncIframePreferences}
          />
        </PublicLayout>
      )
    }

    const contentIsHtml = isLikelyHtml(content)

    if (contentIsHtml) {
      return (
        <PublicLayout showMainContainer={false}>
          <RichContent
            mode='html'
            htmlVariant='isolated'
            content={content}
            className='custom-home-content'
          />
        </PublicLayout>
      )
    }

    return (
      <PublicLayout>
        <div className='mx-auto max-w-6xl px-4 py-8'>
          <RichContent
            mode='markdown'
            content={content}
            className='custom-home-content'
          />
        </div>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout showMainContainer={false}>
      <Hero isAuthenticated={isAuthenticated} />
      {showDeferredSections && (
        <Suspense fallback={<div aria-hidden className='min-h-[40vh]' />}>
          <Stats />
          <Features />
          <HowItWorks />
          <CTA isAuthenticated={isAuthenticated} />
          <Footer />
        </Suspense>
      )}
    </PublicLayout>
  )
}
