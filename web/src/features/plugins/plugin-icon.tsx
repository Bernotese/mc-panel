import { Puzzle } from 'lucide-react'
import { useState } from 'react'
import { iconSrc } from './model'

/** 32-px icon through the panel's icon proxy; a placeholder when there is none or it fails to load. */
export function PluginIcon({ url }: { url: string | null | undefined }) {
  const src = iconSrc(url)
  const [failed, setFailed] = useState<string | null>(null)
  if (!src || failed === src) {
    return (
      <span aria-hidden="true" className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-md">
        <Puzzle className="size-4" />
      </span>
    )
  }
  return (
    <img
      src={src}
      alt=""
      width={32}
      height={32}
      loading="lazy"
      decoding="async"
      className="size-8 shrink-0 rounded-md object-cover"
      onError={() => setFailed(src)}
    />
  )
}
