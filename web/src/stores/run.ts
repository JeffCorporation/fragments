import { defineStore } from 'pinia'
import { api } from '../api/client'
import { usePhotosStore } from './photos'

export interface WorkerStatus {
  id: number
  busy: boolean
  keyBase: string // current item ('' when idle)
  lastKeyBase: string // last item finished (persists; for a preview thumbnail)
}

export interface RunErrorItem {
  keyBase: string
  err: string
}

export interface Snapshot {
  active: boolean
  phase: string
  total: number
  processed: number
  skipped: number
  failed: number
  startedAt: string | null
  elapsedSec: number
  rate: number
  etaSec: number
  workers: WorkerStatus[]
  lastError: string
  errors: RunErrorItem[]
  defaultWorkers: number // effective default concurrency (pre-fills the UI input)
}

export interface StartOptions {
  force?: boolean
  workers?: number
  local?: string
}

// The EventSource is kept at module scope (not in reactive state) so Vue doesn't
// proxy it. connect() is idempotent, so any view can call it safely.
let es: EventSource | null = null

export const useRunStore = defineStore('run', {
  state: () => ({
    snap: null as Snapshot | null,
    connected: false,
    prevPhase: '',
  }),
  getters: {
    progress(state): number {
      const s = state.snap
      if (!s || s.total === 0) return 0
      const done = s.processed + s.skipped + s.failed
      return Math.min(100, Math.round((done / s.total) * 100))
    },
  },
  actions: {
    connect() {
      if (es) return
      es = new EventSource('/api/events', { withCredentials: true })
      es.addEventListener('status', (e) => {
        const snap = JSON.parse((e as MessageEvent).data) as Snapshot
        const wasRunning = this.prevPhase === 'running'
        this.snap = snap
        this.prevPhase = snap.phase
        // When a run finishes, new rows may exist — invalidate the gallery so it
        // refetches the next time it is shown.
        if (wasRunning && (snap.phase === 'done' || snap.phase === 'cancelled')) {
          usePhotosStore().reset()
        }
      })
      es.onopen = () => {
        this.connected = true
      }
      es.onerror = () => {
        this.connected = false
        // Transient network errors auto-reconnect, but a non-200 response (e.g.
        // 401 after the session expired) closes the stream PERMANENTLY. Drop the
        // dead instance so the next connect() — router navigation after
        // re-login — can rebuild it instead of hitting the idempotency guard.
        if (es && es.readyState === EventSource.CLOSED) {
          es.close()
          es = null
        }
      }
    },
    disconnect() {
      es?.close()
      es = null
      this.connected = false
    },
    async start(opts: StartOptions) {
      await api.post('/api/run', opts)
    },
    async cancel() {
      await api.post('/api/run/cancel')
    },
  },
})
