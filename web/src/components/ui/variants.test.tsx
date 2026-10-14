import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { NativeSelect } from '../native-select'
import { Badge } from './badge'
import { Button } from './button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './dialog'
import { Input } from './input'
import { Select, SelectTrigger, SelectValue } from './select'
import { Switch } from './switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './table'
import { Tabs, TabsList, TabsTrigger } from './tabs'

function cls(el: Element): string[] {
  return (el.getAttribute('class') ?? '').split(/\s+/)
}

describe('Button variants', () => {
  it('default is a green raised block with glow and a visible focus outline', () => {
    render(<Button>Start</Button>)
    const b = screen.getByRole('button', { name: 'Start' })
    expect(b).toHaveAttribute('data-variant', 'default')
    expect(cls(b)).toEqual(expect.arrayContaining(['bg-primary', 'text-primary-foreground', 'bevel-primary', 'glow', 'focus-visible:outline-2', 'focus-visible:outline-ring']))
  })
  it('destructive is a solid red block in both modes', () => {
    render(<Button variant="destructive">Löschen</Button>)
    expect(cls(screen.getByRole('button', { name: 'Löschen' }))).toEqual(
      expect.arrayContaining(['bg-destructive-solid', 'text-destructive-solid-foreground', 'bevel-danger']),
    )
  })
  it('outline and secondary are neutral bevels that invert when pressed', () => {
    render(
      <>
        <Button variant="outline">A</Button>
        <Button variant="secondary">B</Button>
      </>,
    )
    for (const name of ['A', 'B']) expect(cls(screen.getByRole('button', { name }))).toEqual(expect.arrayContaining(['bevel', 'active:bevel-pressed']))
  })
  it('ghost and link stay flat', () => {
    render(
      <>
        <Button variant="ghost">G</Button>
        <Button variant="link">L</Button>
      </>,
    )
    for (const name of ['G', 'L']) {
      const c = cls(screen.getByRole('button', { name }))
      expect(c.some((x) => x.startsWith('bevel'))).toBe(false)
    }
  })
  it('keeps data-size and has no rounded-lg left', () => {
    render(<Button size="icon-sm" aria-label="x" />)
    const b = screen.getByRole('button', { name: 'x' })
    expect(b).toHaveAttribute('data-size', 'icon-sm')
    expect(cls(b).some((x) => /rounded-(lg|xl|md)|rounded-\[/.test(x))).toBe(false)
  })
})

describe('Badge', () => {
  it('uses the pixel font at 12 px or more with a bevel', () => {
    render(<Badge>Läuft</Badge>)
    const b = screen.getByText('Läuft')
    expect(cls(b)).toEqual(expect.arrayContaining(['font-heading', 'text-xs', 'uppercase', 'bevel-primary']))
    expect(cls(b).some((x) => /^text-\[(0\.[0-6]|[0-9]px|1[01]px)/.test(x))).toBe(false)
  })
  it('destructive badge is solid red', () => {
    render(<Badge variant="destructive">Fehler</Badge>)
    expect(cls(screen.getByText('Fehler'))).toEqual(expect.arrayContaining(['bg-destructive-solid', 'bevel-danger']))
  })
})

describe('form controls', () => {
  it('input and native select are sunken slots with a focus outline', () => {
    render(
      <>
        <Input aria-label="Name" />
        <NativeSelect aria-label="Typ">
          <option>a</option>
        </NativeSelect>
      </>,
    )
    for (const el of [screen.getByRole('textbox', { name: 'Name' }), screen.getByRole('combobox', { name: 'Typ' })]) {
      expect(cls(el)).toEqual(expect.arrayContaining(['bevel-inset', 'h-9', 'focus-visible:outline-2', 'focus-visible:outline-ring']))
      expect(cls(el).some((x) => /^(dark:)?bg-/.test(x))).toBe(false)
    }
  })
  it('select trigger is a slot', () => {
    render(
      <Select>
        <SelectTrigger aria-label="Version">
          <SelectValue placeholder="–" />
        </SelectTrigger>
      </Select>,
    )
    const trigger = screen.getByRole('combobox', { name: 'Version' })
    expect(trigger).toHaveAttribute('data-slot', 'select-trigger')
    expect(cls(trigger)).toEqual(expect.arrayContaining(['bevel-inset', 'focus-visible:outline-ring']))
  })
  it('switch track shows the checked state in primary and never combines bevel-inset with bg', () => {
    render(<Switch aria-label="An" defaultChecked />)
    const sw = screen.getByRole('switch', { name: 'An' })
    expect(cls(sw)).toEqual(expect.arrayContaining(['data-checked:bg-primary', 'data-unchecked:bg-slot']))
    expect(cls(sw)).not.toContain('bevel-inset')
  })
})

describe('surfaces', () => {
  it('dialog title is a pixel heading inside a bevelled block', () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>Server stoppen?</DialogTitle>
          <DialogDescription>d</DialogDescription>
        </DialogContent>
      </Dialog>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Server stoppen?' })
    expect(cls(dialog)).toContain('bevel')
    expect(cls(screen.getByText('Server stoppen?'))).toContain('pixel-heading')
  })
  it('active tab gets the green underline and keyboard focus stays visible', () => {
    render(
      <Tabs defaultValue="a">
        <TabsList>
          <TabsTrigger value="a">Jails</TabsTrigger>
          <TabsTrigger value="b">Login-Versuche</TabsTrigger>
        </TabsList>
      </Tabs>,
    )
    const active = screen.getByRole('tab', { name: 'Jails' })
    expect(active).toHaveAttribute('data-state', 'active')
    expect(cls(active)).toEqual(expect.arrayContaining(['after:bg-primary', 'data-active:after:opacity-100', 'focus-visible:outline-ring']))
  })
  it('table head uses the pixel label font and rows are dense', () => {
    render(
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Benutzer</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>alice</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )
    expect(cls(screen.getByRole('columnheader', { name: 'Benutzer' }))).toEqual(expect.arrayContaining(['font-heading', 'text-xs', 'uppercase']))
    expect(cls(screen.getByRole('cell', { name: 'alice' }))).toContain('py-1.5')
  })
})
