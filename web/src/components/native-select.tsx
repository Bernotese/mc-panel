import type { SelectHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function NativeSelect({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'bevel-inset text-foreground h-9 rounded-none border-0 px-3 py-1 text-sm outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    />
  )
}
