import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import type { Locale } from '@/api/types'
import { de } from './de'
import { en } from './en'

export const SUPPORTED_LOCALES: readonly Locale[] = ['de', 'en']

/** First supported browser language wins; German is the default. */
export function detectLocale(languages: readonly string[] = typeof navigator === 'undefined' ? [] : navigator.languages): Locale {
  for (const language of languages) {
    const base = language.toLowerCase().split('-')[0]
    if (base === 'de' || base === 'en') return base
  }
  return 'de'
}

export const i18nReady = i18n.use(initReactI18next).init({
  resources: { de: { translation: de }, en: { translation: en } },
  lng: detectLocale(),
  fallbackLng: 'de',
  supportedLngs: [...SUPPORTED_LOCALES],
  interpolation: { escapeValue: false },
})

export function setLocale(locale: Locale): void {
  if (i18n.language !== locale) void i18n.changeLanguage(locale)
  document.documentElement.lang = locale
}

export default i18n
