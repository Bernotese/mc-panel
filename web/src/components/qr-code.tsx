import { useMemo } from 'react'
import { qrPath } from '@/lib/qr'

/**
 * A QR code drawn as SVG elements by React (SEC-48): no data: URL (CSP img-src 'self') and no markup string (Trusted
 * Types). Always black on white, in both themes, so every scanner reads it.
 */
export function QrCode({ text, label, size, className }: { text: string; label: string; size: number; className?: string }) {
  const qr = useMemo(() => qrPath(text), [text])
  return (
    <svg role="img" aria-label={label} width={size} height={size} viewBox={`0 0 ${qr.size} ${qr.size}`} shapeRendering="crispEdges" className={className}>
      <rect width={qr.size} height={qr.size} fill="#fff" />
      <path d={qr.d} fill="#000" />
    </svg>
  )
}
