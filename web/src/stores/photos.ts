import { defineStore } from 'pinia'
import { api, photoPath } from '../api/client'
import type { PhotoItem, PhotoPage } from '../api/client'
import { currentItem } from '../composables/useLightbox'

export interface GalleryFilter {
  minRating?: number
  decision?: string // "keep" (rated) | "discard" | "none" (undecided)
  film?: string
}

interface PhotosState {
  items: PhotoItem[]
  cursor: string
  hasMore: boolean
  loading: boolean
  error: string
  filter: GalleryFilter
  // Incremented by reset(): a page response started under an older epoch is
  // stale (filter changed / gallery refreshed while in flight) and is dropped
  // instead of being appended to the new list.
  epoch: number
}

const PAGE_SIZE = 80

// optimisticTargets returns the distinct on-screen PhotoItem objects for keyBase:
// the gallery-store copy and the lightbox's current item (a different object
// when the lightbox was opened from an album). Deduped by identity.
function optimisticTargets(items: PhotoItem[], keyBase: string): PhotoItem[] {
  const targets: PhotoItem[] = []
  const galleryItem = items.find((i) => i.keyBase === keyBase)
  if (galleryItem) targets.push(galleryItem)
  const cur = currentItem()
  if (cur && cur.keyBase === keyBase && !targets.includes(cur)) targets.push(cur)
  return targets
}

export const usePhotosStore = defineStore('photos', {
  state: (): PhotosState => ({
    items: [],
    cursor: '',
    hasMore: true,
    loading: false,
    error: '',
    filter: {},
    epoch: 0,
  }),
  actions: {
    reset() {
      this.epoch++
      this.items = []
      this.cursor = ''
      this.hasMore = true
      this.error = ''
      // An in-flight load belongs to the old epoch and will be discarded; clear
      // loading so the next loadMore() isn't wrongly debounced.
      this.loading = false
    },
    setFilter(filter: GalleryFilter) {
      this.filter = filter
      this.reset()
      void this.loadMore()
    },
    // refresh re-fetches the gallery from the top with the current filter
    // (e.g. after a catalog run added or changed photos).
    refresh() {
      this.reset()
      void this.loadMore()
    },
    async loadMore() {
      if (this.loading || !this.hasMore) return
      this.loading = true
      this.error = ''
      const epoch = this.epoch
      try {
        const q = new URLSearchParams({ limit: String(PAGE_SIZE) })
        if (this.cursor) q.set('cursor', this.cursor)
        if (this.filter.minRating) q.set('minRating', String(this.filter.minRating))
        if (this.filter.decision) q.set('decision', this.filter.decision)
        if (this.filter.film) q.set('film', this.filter.film)
        const page = await api.get<PhotoPage>('/api/photos?' + q.toString())
        if (epoch !== this.epoch) return // superseded by a reset: drop the stale page
        this.items.push(...page.items)
        this.cursor = page.nextCursor
        this.hasMore = page.nextCursor !== ''
      } catch (e) {
        if (epoch === this.epoch) this.error = e instanceof Error ? e.message : 'Erreur de chargement'
      } finally {
        if (epoch === this.epoch) this.loading = false
      }
    },
    // rating and decision are mutually exclusive (see the backend store): a
    // rating "keeps" and clears any reject; the skull "rejects" and clears the
    // rating. The optimistic update mirrors that so the tile/bar flip instantly,
    // and the snapshot captures BOTH fields so a failed PATCH reverts cleanly.
    async setRating(keyBase: string, rating: number) {
      // Mutate every on-screen copy: the gallery store item AND the lightbox's
      // current item (a distinct object when the lightbox was opened from an
      // album's photo list, not the gallery).
      const targets = optimisticTargets(this.items, keyBase)
      const prev = targets.map((t) => ({ rating: t.rating, decision: t.decision }))
      targets.forEach((t) => {
        t.rating = rating
        if (rating > 0) t.decision = '' // a rating clears any prior reject
      })
      try {
        await api.patch(photoPath(keyBase), { rating })
      } catch (e) {
        targets.forEach((t, i) => ((t.rating = prev[i].rating), (t.decision = prev[i].decision)))
        throw e
      }
    },
    async setDecision(keyBase: string, decision: string) {
      const applied = decision === 'none' ? '' : decision
      const targets = optimisticTargets(this.items, keyBase)
      const prev = targets.map((t) => ({ rating: t.rating, decision: t.decision }))
      targets.forEach((t) => {
        t.decision = applied
        if (applied === 'discard') t.rating = 0 // rejecting clears the rating
      })
      try {
        await api.patch(photoPath(keyBase), { decision })
      } catch (e) {
        targets.forEach((t, i) => ((t.rating = prev[i].rating), (t.decision = prev[i].decision)))
        throw e
      }
    },
  },
})
