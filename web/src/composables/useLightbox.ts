import { reactive, watch, type WatchStopHandle } from 'vue'
import PhotoSwipe from 'photoswipe'
import 'photoswipe/style.css'
import type { PhotoItem } from '../api/client'

// Shared reactive lightbox state. The Vue rating bar (LightboxBar.vue) reads
// `current` to know which photo is shown; openLightbox keeps it in sync with
// PhotoSwipe's current slide so rating/keep-discard/add-to-album act on the
// photo actually on screen. `detailOpen` toggles the on-demand EXIF/Fujifilm
// panel (ExifPanel.vue) for that same photo. `getTotal` is the server-total
// getter for the counter (null → fall back to the loaded length, e.g. albums).
export const lightbox = reactive<{
  open: boolean
  items: PhotoItem[]
  index: number
  detailOpen: boolean
  getTotal: (() => number) | null
}>({
  open: false,
  items: [],
  index: 0,
  detailOpen: false,
  getTotal: null,
})

export function currentItem(): PhotoItem | null {
  if (!lightbox.open) return null
  return lightbox.items[lightbox.index] ?? null
}

// LightboxSource lets the opener wire the lightbox to a paginated backing list.
// The gallery passes both callbacks (epoch-guarded closures over its store);
// albums pass nothing — their list is fully loaded up front.
export interface LightboxSource {
  loadMore?: () => void // fetch the next keyset page (must be re-entrant safe)
  total?: () => number // server-side total for the counter
}

const THUMB_MAX_EDGE = 1024
// Prefetch margin: trigger loadMore this many slides before the loaded end.
// PAGE_SIZE is 80 and auto-advance is ≥150 ms/photo, so 20 gives ~3 s of
// network headroom before navigation could hit the edge.
const LOAD_AHEAD = 20

let pswp: PhotoSwipe | null = null
// The live array PhotoSwipe reads through getNumItems() — pushing onto it
// makes new slides navigable without rebuilding the lightbox.
let dataSource: { src: string; width: number; height: number; alt: string }[] = []
let growable = false // a loadMore source exists (gallery, not album)
let stalledAdvance = false // auto-advance hit the loaded end; resume on append
let stopGrowWatch: WatchStopHandle | null = null

function toSlide(i: PhotoItem) {
  const w = i.width || 1500
  const h = i.height || 1000
  const scale = Math.min(1, THUMB_MAX_EDGE / Math.max(w, h))
  return {
    src: i.thumbUrl,
    width: Math.round(w * scale),
    height: Math.round(h * scale),
    alt: i.name,
  }
}

// advanceLightbox moves to the next photo, used by the keyboard culling
// shortcuts (rate / reject auto-advance). It stays put on the last photo rather
// than wrapping (loop is off). On a growable lightbox the last *loaded* photo
// is not the real end: remember the intent and resume when the page lands.
export function advanceLightbox(): void {
  if (!pswp) return
  if (pswp.currIndex < pswp.getNumItems() - 1) pswp.next()
  else if (growable) stalledAdvance = true
}

export function openLightbox(items: PhotoItem[], index: number, source: LightboxSource = {}): void {
  dataSource = items.map(toSlide)
  growable = !!source.loadMore

  pswp = new PhotoSwipe({
    dataSource,
    index,
    bgOpacity: 1,
    showHideAnimationType: 'fade',
    // The gallery set is partial: wrapping at the loaded end would jump back to
    // photo 1 while more photos exist server-side. Stop at the ends instead.
    loop: false,
    // The native counter can only show dataSource.length (the loaded count);
    // LightboxBar renders its own "n / N" from the server total.
    counter: false,
  })
  // Keep the shared array reference (same objects as the gallery store) so a
  // rating change made in the bar is reflected in the grid too.
  lightbox.items = items
  lightbox.index = index
  lightbox.detailOpen = false // each new lightbox session starts with the panel closed
  lightbox.getTotal = source.total ?? null

  pswp.on('change', () => {
    if (!pswp) return
    if (lightbox.index !== pswp.currIndex) stalledAdvance = false // manual nav voids a stalled advance
    lightbox.index = pswp.currIndex
    if (source.loadMore && pswp.currIndex >= dataSource.length - LOAD_AHEAD) source.loadMore()
  })

  // Pages fetched while browsing are pushed onto the SAME dataSource array
  // (getNumItems() re-reads its length). refreshSlideContent(prevLen) rebuilds
  // the "next" holder if it was created empty at the old end, and redispatches
  // 'change' → re-checks the load-ahead threshold (chained catch-up, bounded by
  // the store's loading/hasMore guards). After a store reset the store replaces
  // its array, so this orphaned one never grows again — no stale appends.
  stopGrowWatch = watch(
    () => lightbox.items.length,
    (len) => {
      if (!pswp || len <= dataSource.length) return
      const prevLen = dataSource.length
      for (let i = prevLen; i < len; i++) dataSource.push(toSlide(lightbox.items[i]))
      pswp.refreshSlideContent(prevLen)
      if (stalledAdvance) {
        stalledAdvance = false
        pswp.next()
      }
    },
  )

  pswp.on('destroy', () => {
    lightbox.open = false
    lightbox.detailOpen = false
    lightbox.getTotal = null
    stopGrowWatch?.()
    stopGrowWatch = null
    growable = false
    stalledAdvance = false
    pswp = null
  })

  pswp.init()
  lightbox.open = true
}
