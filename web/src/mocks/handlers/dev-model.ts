import type { DevInstance, DevSummary } from '@/api/types'
import { db, devInstanceConfig, nextDailyAt, stoppedStatus, type MockDev, type MockServer } from '../db'

/** A mock sync (snapshot, stop, restore, apply) takes about this long. */
export const DEV_SYNC_MS = 4000
export const DEV_PREFIX = 'dev-'
export const MAX_SERVER_NAME = 32

/** The dev of prodId, if it has one. */
export function devOf(prodId: number): MockServer | undefined {
  return db.servers.find((s) => s.dev?.sourceId === prodId)
}

export function nextSyncAt(dev: MockDev, now: number): number | null {
  return dev.syncPaused ? null : nextDailyAt(dev.syncTime, db.serverSettings.backup_timezone, now)
}

/** Like servers.ApplyDevSpec: prod's spec plus the dev overrides, the dev's memory kept, revision bumped. */
export function applyDevSpec(server: MockServer): void {
  const dev = server.dev
  const source = dev ? db.servers.find((s) => s.id === dev.sourceId) : undefined
  if (!dev || !source?.instance || !server.instance) return
  server.instance = devInstanceConfig(source.name, source.instance, server.instance.memory_mib, dev)
  server.revision += 1
  if (server.status.state === 'running') server.status.restart_required = true
  else server.runningRevision = server.revision
}

/** Finishes a sync whose time is up: the dev was stopped for it and stays asleep until the next connection. */
export function settleDev(server: MockServer, now = Date.now()): void {
  const dev = server.dev
  if (!dev || dev.syncSince === null || now - dev.syncSince < DEV_SYNC_MS) return
  dev.syncSince = null
  dev.lastSyncAt = now
  const source = db.servers.find((s) => s.id === dev.sourceId)
  if (!source?.instance) {
    dev.lastSyncStatus = 'failed'
    dev.lastSyncError = 'the source server has no instance'
    return
  }
  server.status = stoppedStatus()
  dev.sleeping = true
  applyDevSpec(server)
  dev.lastSyncStatus = 'ok'
  dev.lastSyncError = null
  dev.nextSyncAt = nextSyncAt(dev, now)
}

function sourceName(dev: MockDev): string {
  return db.servers.find((s) => s.id === dev.sourceId)?.name ?? ''
}

export function devSummary(dev: MockDev): DevSummary {
  return {
    source_id: dev.sourceId,
    source_name: sourceName(dev),
    sync_paused: dev.syncPaused,
    next_sync_at: dev.nextSyncAt,
    last_sync_at: dev.lastSyncAt,
    last_sync_status: dev.lastSyncStatus,
    sync_pending: dev.syncSince !== null,
    sleeping: dev.sleeping,
  }
}

export function devBody(server: MockServer, dev: MockDev): DevInstance {
  return {
    ...devSummary(dev),
    server_id: server.id,
    paused_at: dev.pausedAt,
    sync_time: dev.syncTime,
    idle_minutes: dev.idleMinutes,
    view_distance: dev.viewDistance,
    simulation_distance: dev.simulationDistance,
    last_sync_error: dev.lastSyncError,
  }
}
