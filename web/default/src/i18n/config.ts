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
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

import {
  convertDetectedLanguage,
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
  type InterfaceLanguageCode,
} from './languages'
import en from './locales/en.json'

type TranslationBundle = typeof en

const languageLoaders: Record<
  Exclude<InterfaceLanguageCode, 'en'>,
  () => Promise<{ default: TranslationBundle }>
> = {
  zhCN: () => import('./locales/zh.json'),
  fr: () => import('./locales/fr.json'),
  ru: () => import('./locales/ru.json'),
  ja: () => import('./locales/ja.json'),
  vi: () => import('./locales/vi.json'),
  zhTW: () => import('./locales/zh-TW.json'),
}

function getInitialLanguage(): InterfaceLanguageCode {
  let detected = 'en'
  try {
    detected =
      localStorage.getItem('i18nextLng') ||
      navigator.languages?.[0] ||
      navigator.language ||
      'en'
  } catch {
    detected = 'en'
  }

  const converted = convertDetectedLanguage(detected)
  const exact = INTERFACE_LANGUAGE_OPTIONS.find(
    (option) => option.code === converted
  )
  if (exact) return exact.code

  const base = converted.split('-')[0]
  return normalizeInterfaceLanguage(base) as InterfaceLanguageCode
}

async function loadLanguageBundle(
  language: InterfaceLanguageCode
): Promise<TranslationBundle> {
  if (language === 'en') return en
  return (await languageLoaders[language]()).default
}

async function initializeI18n(): Promise<void> {
  const language = getInitialLanguage()
  const bundle = await loadLanguageBundle(language)
  await i18n.use(initReactI18next).init({
    resources: {
      en,
      ...(language === 'en' ? {} : { [language]: bundle }),
    },
    lng: language,
    fallbackLng: 'en',
    supportedLngs: INTERFACE_LANGUAGE_OPTIONS.map((option) => option.code),
    load: 'currentOnly',
    nsSeparator: false,
    debug: import.meta.env.DEV,
    interpolation: {
      escapeValue: false,
    },
  })
}

export const i18nReady = initializeI18n().catch(async () => {
  if (i18n.isInitialized) {
    await i18n.changeLanguage('en')
    return
  }
  await i18n.use(initReactI18next).init({
    resources: { en },
    lng: 'en',
    fallbackLng: 'en',
    nsSeparator: false,
    interpolation: { escapeValue: false },
  })
})

export async function changeInterfaceLanguage(value: string): Promise<void> {
  const language = normalizeInterfaceLanguage(value) as InterfaceLanguageCode
  if (!i18n.hasResourceBundle(language, 'translation')) {
    const bundle = await loadLanguageBundle(language)
    i18n.addResourceBundle(language, 'translation', bundle.translation, true)
  }
  try {
    localStorage.setItem('i18nextLng', language)
  } catch {
    /* empty */
  }
  await i18n.changeLanguage(language)
}

export default i18n
