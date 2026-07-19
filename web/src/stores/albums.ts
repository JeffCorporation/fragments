import { defineStore } from 'pinia'
import { api, encodeKeyBase } from '../api/client'

export interface Album {
  id: number
  name: string
  createdAt: string
  photoCount: number
  coverThumbUrl: string
}

export const useAlbumsStore = defineStore('albums', {
  state: () => ({
    list: [] as Album[],
    loaded: false,
    loading: false,
  }),
  actions: {
    async fetch() {
      this.loading = true
      try {
        const r = await api.get<{ albums: Album[] }>('/api/albums')
        this.list = r.albums ?? []
        this.loaded = true
      } finally {
        this.loading = false
      }
    },
    async ensure() {
      if (!this.loaded && !this.loading) await this.fetch()
    },
    async create(name: string): Promise<Album> {
      const album = await api.post<Album>('/api/albums', { name })
      this.list.unshift(album)
      return album
    },
    async remove(id: number) {
      await api.del(`/api/albums/${id}`)
      this.list = this.list.filter((a) => a.id !== id)
    },
    async addPhoto(id: number, keyBase: string) {
      const r = await api.post<{ added: boolean }>(`/api/albums/${id}/photos`, { keyBase })
      // Only bump the count when a row was actually inserted (the add is
      // idempotent server-side, so re-adding a member is a no-op).
      if (r?.added) {
        const a = this.list.find((x) => x.id === id)
        if (a) a.photoCount += 1
      }
    },
    async removePhoto(id: number, keyBase: string) {
      await api.del(`/api/albums/${id}/photos/${encodeKeyBase(keyBase)}`)
      const a = this.list.find((x) => x.id === id)
      if (a && a.photoCount > 0) a.photoCount -= 1
    },
    async reorder(id: number, keyBases: string[]) {
      await api.patch(`/api/albums/${id}/order`, { keyBases })
    },
  },
})
