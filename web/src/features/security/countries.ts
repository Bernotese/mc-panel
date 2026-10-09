const COUNTRY = /^[A-Z]{2}$/

export function parseCountries(input: string): { codes: string[]; invalid: string[] } {
  const tokens = input
    .split(/[\s,;]+/)
    .map((token) => token.trim().toUpperCase())
    .filter(Boolean)
  const unique = [...new Set(tokens)]
  return { codes: unique.filter((c) => COUNTRY.test(c)), invalid: unique.filter((c) => !COUNTRY.test(c)) }
}

export function formatCountries(codes: string[]): string {
  return codes.join(', ')
}
