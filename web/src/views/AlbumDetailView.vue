<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import draggable from 'vuedraggable'
import { NButton, NSwitch, useMessage } from 'naive-ui'
import NavBar from '../components/NavBar.vue'
import { api } from '../api/client'
import type { PhotoItem } from '../api/client'
import type { Album } from '../stores/albums'
import { useAlbumsStore } from '../stores/albums'
import { openLightbox } from '../composables/useLightbox'

const route = useRoute()
const albums = useAlbumsStore()
const message = useMessage()

const albumId = computed(() => Number(route.params.id))
const album = ref<Album | null>(null)
const items = ref<PhotoItem[]>([])
const loading = ref(true)
const includeRaw = ref(false)

function exportAlbum() {
  // GET download: the browser sends the session cookie (same-origin) and the
  // Content-Disposition header makes it a file download without navigating away.
  window.location.href = `/api/albums/${albumId.value}/export` + (includeRaw.value ? '?raw=true' : '')
}

async function load() {
  loading.value = true
  try {
    const r = await api.get<{ album: Album; items: PhotoItem[] }>(`/api/albums/${albumId.value}`)
    album.value = r.album
    items.value = r.items
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec du chargement')
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function onReorder() {
  try {
    await albums.reorder(
      albumId.value,
      items.value.map((i) => i.keyBase),
    )
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec du réordonnancement')
    void load()
  }
}

async function removeOne(keyBase: string) {
  const prev = items.value
  items.value = items.value.filter((i) => i.keyBase !== keyBase)
  try {
    await albums.removePhoto(albumId.value, keyBase)
  } catch (e) {
    items.value = prev
    message.error(e instanceof Error ? e.message : 'Échec du retrait')
  }
}

function open(index: number) {
  openLightbox(items.value, index)
}
</script>

<template>
  <div class="page">
    <NavBar>
      <span v-if="album" class="count">{{ album.name }} · {{ items.length }}</span>
    </NavBar>
    <div class="content">
      <div class="album-toolbar">
        <span class="inline-switch"><n-switch v-model:value="includeRaw" /> inclure les fichiers RAW</span>
        <n-button size="small" type="primary" :disabled="items.length === 0" @click="exportAlbum">
          Exporter (zip)
        </n-button>
      </div>
      <p class="hint">Glisse-dépose pour réordonner (l’ordre est conservé pour l’export).</p>
      <div v-if="loading" class="status">chargement…</div>
      <div v-else-if="items.length === 0" class="status">
        Album vide. Ajoute des photos depuis la visionneuse.
      </div>
      <draggable
        v-else
        v-model="items"
        item-key="keyBase"
        class="album-detail-grid"
        :animation="150"
        @end="onReorder"
      >
        <template #item="{ element, index }">
          <div class="ad-tile">
            <img :src="element.thumbUrl" :alt="element.name" loading="lazy" @click="open(index)" />
            <button class="ad-del" title="Retirer de l’album" @click.stop="removeOne(element.keyBase)">✕</button>
            <span v-if="element.rating > 0" class="ad-rating">{{ '★'.repeat(element.rating) }}</span>
          </div>
        </template>
      </draggable>
    </div>
  </div>
</template>
