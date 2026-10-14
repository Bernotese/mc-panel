import { Fragment, type ReactNode } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DESKTOP_QUERY, useMediaQuery } from '@/lib/use-media-query'

export interface Column<T> {
  id: string
  header: string
  cell(row: T): ReactNode
  className?: string
}

interface DataListProps<T> {
  items: T[]
  columns: Column<T>[]
  rowKey(row: T): string | number
  caption: string
  empty: string
}

/** Table on desktop, one card per row below the md breakpoint (usable from 360px width). */
export function DataList<T>({ items, columns, rowKey, caption, empty }: DataListProps<T>) {
  const desktop = useMediaQuery(DESKTOP_QUERY)
  if (items.length === 0) {
    return <p className="text-muted-foreground py-6 text-center text-sm">{empty}</p>
  }
  if (desktop) {
    return (
      <Table>
        <TableCaption className="sr-only">{caption}</TableCaption>
        <TableHeader>
          <TableRow>
            {columns.map((c) => (
              <TableHead key={c.id} scope="col" className={c.className}>
                {c.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={rowKey(item)}>
              {columns.map((c) => (
                <TableCell key={c.id} className={c.className}>
                  {c.cell(item)}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }
  return (
    <ul aria-label={caption} className="space-y-2">
      {items.map((item) => (
        <li key={rowKey(item)}>
          <Card size="sm">
            <CardContent>
              <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1.5 text-sm">
                {columns.map((c) => (
                  <Fragment key={c.id}>
                    <dt className="text-muted-foreground">{c.header}</dt>
                    <dd className="min-w-0 break-words">{c.cell(item)}</dd>
                  </Fragment>
                ))}
              </dl>
            </CardContent>
          </Card>
        </li>
      ))}
    </ul>
  )
}
