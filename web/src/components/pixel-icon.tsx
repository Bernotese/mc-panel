import { cn } from '@/lib/utils'

export type PixelIconKind = 'grass' | 'server'

/** Own pixel art (no Mojang assets): a grass block and a "server" block with a status lamp. */
export function PixelIcon({
  kind,
  state,
  size = 20,
  className,
}: {
  kind: PixelIconKind
  state?: 'online' | 'offline' | 'unknown'
  size?: number
  className?: string
}) {
  return (
    <svg
      aria-hidden="true"
      data-kind={kind}
      data-state={state}
      viewBox="0 0 16 16"
      width={size}
      height={size}
      shapeRendering="crispEdges"
      style={{ width: `${size}px`, height: `${size}px` }}
      className={cn('shrink-0', state === 'offline' && 'opacity-50 grayscale', className)}
    >
      {kind === 'grass' ? (
        <>
          <rect width="16" height="16" fill="#866043" />
          <rect width="16" height="6" fill="#5cc14a" />
          <rect y="6" width="16" height="2" fill="#3f8f2f" />
          <rect x="2" y="6" width="2" height="3" fill="#5cc14a" />
          <rect x="9" y="6" width="2" height="4" fill="#5cc14a" />
          <rect x="5" y="11" width="2" height="2" fill="#6b4c33" />
          <rect x="12" y="12" width="2" height="2" fill="#6b4c33" />
          <rect width="16" height="1" fill="#ffffff" opacity="0.2" />
          <rect y="15" width="16" height="1" fill="#000000" opacity="0.25" />
        </>
      ) : (
        <>
          <rect width="16" height="16" fill="#3a3d45" />
          <rect x="1" y="1" width="14" height="6" fill="#575b66" />
          <rect x="1" y="9" width="14" height="6" fill="#575b66" />
          <rect x="3" y="3" width="6" height="2" fill="#24262c" />
          <rect x="3" y="11" width="6" height="2" fill="#24262c" />
          <rect x="11" y="3" width="2" height="2" fill={state === 'online' ? '#5cc14a' : state === 'offline' ? '#8a8d96' : '#f5c451'} />
          <rect x="11" y="11" width="2" height="2" fill={state === 'online' ? '#5cc14a' : state === 'offline' ? '#8a8d96' : '#f5c451'} />
        </>
      )}
    </svg>
  )
}
