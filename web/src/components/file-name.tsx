import { visibleName } from '@/lib/file-name'

/** A file name in its own bidi context with visible format characters (SEC-38). */
export function FileName({ name, className, title }: { name: string; className?: string; title?: string }) {
  return (
    <bdi className={className} title={title}>
      {visibleName(name)}
    </bdi>
  )
}
