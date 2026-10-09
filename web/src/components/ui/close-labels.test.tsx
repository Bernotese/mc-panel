import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from './dialog'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from './sheet'

describe('close buttons', () => {
  it('translates the dialog close button', () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>t</DialogTitle>
          <DialogDescription>d</DialogDescription>
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>,
    )
    expect(screen.getAllByRole('button', { name: 'Schließen' })).toHaveLength(2)
    expect(screen.queryByText('Close')).not.toBeInTheDocument()
  })

  it('translates the sheet close button', () => {
    render(
      <Sheet open>
        <SheetContent>
          <SheetTitle>t</SheetTitle>
          <SheetDescription>d</SheetDescription>
        </SheetContent>
      </Sheet>,
    )
    expect(screen.getByRole('button', { name: 'Schließen' })).toBeInTheDocument()
  })
})
