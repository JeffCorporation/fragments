<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { NButton, NDropdown, useMessage } from 'naive-ui'
import type { DropdownOption } from 'naive-ui'
import { useAlbumsStore } from '../stores/albums'

const props = defineProps<{ keyBase: string }>()
const albums = useAlbumsStore()
const message = useMessage()

onMounted(() => void albums.ensure())

const options = computed<DropdownOption[]>(() => [
  ...albums.list.map((a) => ({ label: `${a.name} (${a.photoCount})`, key: String(a.id) })),
  ...(albums.list.length ? [{ type: 'divider', key: 'd' } as DropdownOption] : []),
  { label: '+ Nouvel album…', key: '__new' },
])

async function onSelect(key: string) {
  try {
    if (key === '__new') {
      const name = window.prompt('Nom du nouvel album ?')?.trim()
      if (!name) return
      const album = await albums.create(name)
      await albums.addPhoto(album.id, props.keyBase)
      message.success(`Ajouté à « ${album.name} »`)
      return
    }
    const id = Number(key)
    await albums.addPhoto(id, props.keyBase)
    const album = albums.list.find((a) => a.id === id)
    message.success(`Ajouté à « ${album?.name ?? 'album'} »`)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de l’ajout')
  }
}
</script>

<template>
  <!-- z-index au-dessus de PhotoSwipe (100000) et de la barre (100001) : sinon le
       menu, téléporté dans <body> avec un z-index naive-ui par défaut (~2000),
       s'afficherait DERRIÈRE le lightbox et serait invisible. -->
  <n-dropdown trigger="click" :options="options" :z-index="100003" @select="onSelect">
    <!-- Le parent peut fournir son propre déclencheur (ex. bouton-icône de la
         barre du lightbox) ; sinon on garde le bouton « + Album » par défaut. -->
    <slot>
      <n-button size="small">+ Album</n-button>
    </slot>
  </n-dropdown>
</template>
