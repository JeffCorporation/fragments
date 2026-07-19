import { reactive } from 'vue'
import PhotoSwipe from 'photoswipe'
import 'photoswipe/style.css'
import type { PhotoItem } from '../api/client'

// Shared reactive lightbox state. The Vue rating bar (LightboxBar.vue) reads
// `current` to know which photo is shown; openLightbox keeps it in sync with
// PhotoSwipe's current slide so rating/keep-discard/add-to-album act on the
// photo actually on screen. `detailOpen` toggles the on-demand EXIF/Fujifilm
// panel (ExifPanel.vue) for that same photo.
export const lightbox = reactive<{
  open: boolean
  items: PhotoItem[]
  index: number
  detailOpen: boolean
}>({
  open: false,
  items: [],
  index: 0,
  detailOpen: false,
})

export function currentItem(): PhotoItem | null {
  if (!lightbox.open) return null
  return lightbox.items[lightbox.index] ?? null
}

const THUMB_MAX_EDGE = 1024
let pswp: PhotoSwipe | null = null

// advanceLightbox moves to the next photo, used by the keyboard culling
// shortcuts (rate / reject auto-advance). It stays put on the last photo rather
// than wrapping — PhotoSwipe's `loop` default is unset/ambiguous, so we gate on
// the index explicitly. The existing 'change' handler resyncs lightbox.index.
export function advanceLightbox(): void {
  if (!pswp) return
  if (pswp.currIndex < lightbox.items.length - 1) pswp.next()
}

export function openLightbox(items: PhotoItem[], index: number): void {
  const dataSource = items.map((i) => {
    const w = i.width || 1500
    const h = i.height || 1000
    const scale = Math.min(1, THUMB_MAX_EDGE / Math.max(w, h))
    return {
      src: i.thumbUrl,
      width: Math.round(w * scale),
      height: Math.round(h * scale),
      alt: i.name,
    }
  })

  pswp = new PhotoSwipe({ dataSource, index, bgOpacity: 1, showHideAnimationType: 'fade' })
  // Keep the shared array reference (same objects as the gallery store) so a
  // rating change made in the bar is reflected in the grid too.
  lightbox.items = items
  lightbox.index = index
  lightbox.detailOpen = false // each new lightbox session starts with the panel closed

  pswp.on('change', () => {
    if (pswp) lightbox.index = pswp.currIndex
  })
  pswp.on('destroy', () => {
    lightbox.open = false
    lightbox.detailOpen = false
    pswp = null
  })

  pswp.init()
  lightbox.open = true
}
