<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, useMessage } from 'naive-ui'
import NavBar from '../components/NavBar.vue'
import { useAlbumsStore } from '../stores/albums'

const albums = useAlbumsStore()
const router = useRouter()
const message = useMessage()

onMounted(() => void albums.fetch())

async function createAlbum() {
  const name = window.prompt('Nom du nouvel album ?')?.trim()
  if (!name) return
  try {
    const a = await albums.create(name)
    void router.push({ name: 'album', params: { id: a.id } })
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de la création')
  }
}

async function removeAlbum(id: number, name: string, ev: Event) {
  ev.stopPropagation()
  if (!window.confirm(`Supprimer l’album « ${name} » ? (les photos ne sont pas supprimées)`)) return
  try {
    await albums.remove(id)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de la suppression')
  }
}

function open(id: number) {
  void router.push({ name: 'album', params: { id } })
}
</script>

<template>
  <div class="page">
    <NavBar>
      <n-button size="small" type="primary" @click="createAlbum">+ Nouvel album</n-button>
    </NavBar>
    <div class="content">
      <div v-if="albums.loaded && albums.list.length === 0" class="status">
        Aucun album. Crée-en un, ou ajoute des photos depuis la visionneuse (bouton « + Album »).
      </div>
      <div class="album-grid">
        <div v-for="a in albums.list" :key="a.id" class="album-card" @click="open(a.id)">
          <div class="album-cover">
            <img v-if="a.coverThumbUrl" :src="a.coverThumbUrl" :alt="a.name" loading="lazy" />
            <div v-else class="album-cover--empty">vide</div>
          </div>
          <div class="album-meta">
            <span class="album-name">{{ a.name }}</span>
            <span class="album-count">{{ a.photoCount }} photo{{ a.photoCount > 1 ? 's' : '' }}</span>
          </div>
          <button class="album-del" title="Supprimer l’album" @click="removeAlbum(a.id, a.name, $event)">✕</button>
        </div>
      </div>
    </div>
  </div>
</template>
