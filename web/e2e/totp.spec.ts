import { expect, test } from '@playwright/test'
import { base32Decode, totp } from './totp'

// ASCII "12345678901234567890" in base32
const SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'

const VECTORS: Array<[number, string]> = [
  [59, '94287082'],
  [1111111109, '07081804'],
  [1111111111, '14050471'],
  [1234567890, '89005924'],
  [2000000000, '69279037'],
  [20000000000, '65353130'],
]

test('decodes base32', () => {
  expect(base32Decode(SECRET).toString('ascii')).toBe('12345678901234567890')
  expect(base32Decode('gezd gnbv')).toEqual(base32Decode('GEZDGNBV'))
})

for (const [seconds, code] of VECTORS) {
  test(`RFC 6238 vector T=${seconds}`, () => {
    expect(totp(SECRET, seconds * 1000, 30, 8)).toBe(code)
  })
}

test('defaults to 6 digits', () => {
  expect(totp(SECRET, 59_000)).toBe('287082')
})
