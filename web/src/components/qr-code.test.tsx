import { render, screen } from '@testing-library/react'
import QRCode from 'qrcode'
import { describe, expect, it } from 'vitest'
import { qrPath } from '@/lib/qr'
import { QrCode } from './qr-code'

const URL = 'otpauth://totp/mc-panel:admin?secret=JBSWY3DPEHPK3PXP&issuer=mc-panel'

describe('QrCode (SEC-48)', () => {
  it('draws exactly the dark modules, inside a one-module quiet zone', () => {
    const { modules } = QRCode.create(URL, { errorCorrectionLevel: 'M' })
    const { size, d } = qrPath(URL)
    expect(size).toBe(modules.size + 2)
    const squares = d.match(/M\d+ \d+h1v1h-1z/g) ?? []
    expect(squares.join('')).toBe(d)
    expect(squares).toHaveLength(Array.from(modules.data).filter(Boolean).length)
    // The top-left finder pattern starts at module (0,0), drawn at (1,1).
    expect(d.startsWith('M1 1h1v1h-1z')).toBe(true)
  })

  it('renders an accessible SVG without any URL', () => {
    const { container } = render(<QrCode text={URL} label="QR-Code" size={224} />)
    const svg = screen.getByRole('img', { name: 'QR-Code' })
    expect(svg).toHaveAttribute('width', '224')
    expect(container.innerHTML).not.toMatch(/data:|href=/)
  })
})
