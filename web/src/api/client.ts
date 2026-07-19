// Tiny typed fetch wrapper. All requests are same-origin (Vite proxies the
// backend in dev; the SPA is embedded in prod), so cookies just need
// credentials:'include'. The CSRF token from login is echoed on unsafe methods.

export interface PhotoItem {
  keyBase: string
  name: string
  folder: string
  takenAt: string | null
  width: number
  height: number
  cameraModel: string
  lensModel: string
  iso: number
  fNumber: number
  exposureTime: string
  focalLength: number
  filmSimulation: string
  rating: number
  decision: string // '' (undecided / kept via rating) | 'discard' (rejected)
  thumbUrl: string
}

export interface PhotoDetail extends PhotoItem {
  jpegKey: string
  rafKey: string
  gpsLat: number | null
  gpsLon: number | null
  exifJson: string
}

export interface PhotoPage {
  items: PhotoItem[]
  nextCursor: string
}

export interface ApiError extends Error {
  status: number
}

let csrfToken = ''
export function setCsrf(token: string): void {
  csrfToken = token
}

// onUnauthorized is invoked when an authenticated request returns 401 (a session
// that expired mid-browse). Registered in router.ts to reset auth state and
// redirect to login. The boot probes (/api/me, /api/login) are excluded so they
// don't trigger a redirect during normal login flow.
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn
}

async function request<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers = new Headers(opts.headers)
  const method = (opts.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  if (opts.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const res = await fetch(path, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    if (
      res.status === 401 &&
      onUnauthorized &&
      !path.startsWith('/api/me') &&
      !path.startsWith('/api/login')
    ) {
      onUnauthorized()
    }
    let message = res.statusText
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {
      // non-JSON error body; keep statusText
    }
    const err = new Error(message) as ApiError
    err.status = res.status
    throw err
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string): Promise<T> => request<T>(path),
  post: <T>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
  patch: <T>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, { method: 'PATCH', body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(path: string): Promise<T> => request<T>(path, { method: 'DELETE' }),
}

// encodeKeyBase percent-encodes each path segment of a key_base while keeping
// the structural '/' raw (the backend route is a wildcard that wants the slash).
// This keeps keys containing reserved characters (#, ?, %, space) from corrupting
// the URL.
export function encodeKeyBase(keyBase: string): string {
  return keyBase.split('/').map(encodeURIComponent).join('/')
}

// photoPath builds the API path for a single photo.
export function photoPath(keyBase: string): string {
  return '/api/photos/' + encodeKeyBase(keyBase)
}
