import QRCode from 'qrcode'

const QUIET_ZONE = 1

/** The dark modules of a QR code as one SVG path in module units, offset by the quiet zone. */
export function qrPath(text: string): { size: number; d: string } {
  const { modules } = QRCode.create(text, { errorCorrectionLevel: 'M' })
  let d = ''
  for (let row = 0; row < modules.size; row++) {
    for (let col = 0; col < modules.size; col++) {
      if (modules.get(row, col)) d += `M${col + QUIET_ZONE} ${row + QUIET_ZONE}h1v1h-1z`
    }
  }
  return { size: modules.size + 2 * QUIET_ZONE, d }
}
