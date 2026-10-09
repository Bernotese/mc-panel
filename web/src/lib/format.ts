import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

export function formatDateTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(ms))
}

export function formatDate(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(ms))
}

export function formatTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' }).format(new Date(ms))
}

export function formatRelative(ms: number, locale: string, now: number = Date.now()): string {
  const diff = ms - now
  const abs = Math.abs(diff)
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  if (abs < 60_000) return rtf.format(Math.round(diff / 1000), 'second')
  if (abs < 3_600_000) return rtf.format(Math.round(diff / 60_000), 'minute')
  if (abs < 86_400_000) return rtf.format(Math.round(diff / 3_600_000), 'hour')
  return rtf.format(Math.round(diff / 86_400_000), 'day')
}

export function formatNumber(n: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(n)
}

export function useFormat() {
  const { i18n } = useTranslation()
  const locale = i18n.language
  return useMemo(
    () => ({
      locale,
      dateTime: (ms: number) => formatDateTime(ms, locale),
      date: (ms: number) => formatDate(ms, locale),
      time: (ms: number) => formatTime(ms, locale),
      relative: (ms: number) => formatRelative(ms, locale),
      number: (n: number) => formatNumber(n, locale),
    }),
    [locale],
  )
}
