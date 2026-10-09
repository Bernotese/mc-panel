/**
 * Props for a DialogContent that must not be dismissed (X, Escape, click outside) while `locked` is true,
 * e.g. while one-time recovery codes are shown and not yet confirmed. Pair with {@link guardClose}.
 */
export function dialogLock(locked: boolean) {
  return {
    showCloseButton: !locked,
    onEscapeKeyDown: (event: Event) => {
      if (locked) event.preventDefault()
    },
    onInteractOutside: (event: Event) => {
      if (locked) event.preventDefault()
    },
  }
}

/** Wraps an onOpenChange so closing is ignored while locked. */
export function guardClose(locked: boolean, onOpenChange: (open: boolean) => void) {
  return (open: boolean) => {
    if (!open && locked) return
    onOpenChange(open)
  }
}
