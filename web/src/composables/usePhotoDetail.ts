import { api, photoPath } from '../api/client'
import type { PhotoDetail } from '../api/client'

// Module-level cache for the full per-photo detail (the heavy exif_json payload
// the gallery list omits). Fetched lazily when the EXIF panel is opened and kept
// for the session so re-opening or swiping back is instant. In-flight requests
// are deduped so a fast double-toggle issues a single fetch.
const cache = new Map<string, PhotoDetail>()
const inflight = new Map<string, Promise<PhotoDetail>>()

export async function loadPhotoDetail(keyBase: string): Promise<PhotoDetail> {
  const cached = cache.get(keyBase)
  if (cached) return cached
  const pending = inflight.get(keyBase)
  if (pending) return pending

  const req = api
    .get<PhotoDetail>(photoPath(keyBase))
    .then((d) => {
      cache.set(keyBase, d)
      inflight.delete(keyBase)
      return d
    })
    .catch((e) => {
      inflight.delete(keyBase)
      throw e
    })
  inflight.set(keyBase, req)
  return req
}
