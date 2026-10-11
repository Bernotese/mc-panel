type Listener = (event: Event) => void

/** EventSource stand-in for jsdom (which has none). Tests drive it with open(), emit() and fail(). */
export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  readonly url: string
  readyState: number = FakeEventSource.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  private readonly listeners = new Map<string, Set<Listener>>()

  constructor(url: string | URL) {
    this.url = String(url)
    FakeEventSource.instances.push(this)
  }

  static reset(): void {
    FakeEventSource.instances = []
  }

  static latest(): FakeEventSource {
    const latest = FakeEventSource.instances.at(-1)
    if (!latest) throw new Error('no EventSource was opened')
    return latest
  }

  addEventListener(type: string, listener: Listener): void {
    let set = this.listeners.get(type)
    if (!set) {
      set = new Set()
      this.listeners.set(type, set)
    }
    set.add(listener)
  }

  removeEventListener(type: string, listener: Listener): void {
    this.listeners.get(type)?.delete(listener)
  }

  close(): void {
    this.readyState = FakeEventSource.CLOSED
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.(new Event('open'))
  }

  emit(type: string, data: unknown, lastEventId = ''): void {
    const event = new MessageEvent(type, { data: JSON.stringify(data), lastEventId })
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }

  /** closed = true: the browser gave up (non-200 answer); false: it will reconnect by itself. */
  fail(closed: boolean): void {
    this.readyState = closed ? FakeEventSource.CLOSED : FakeEventSource.CONNECTING
    this.onerror?.(new Event('error'))
  }
}
