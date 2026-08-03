<script setup lang="ts">
import { onMounted, watch, ref, computed } from 'vue'
import { NSpin, NSelect, NRate, NButton } from 'naive-ui'
import { usePhotosStore } from '../stores/photos'
import { useRecipesStore } from '../stores/recipes'
import NavBar from '../components/NavBar.vue'
import JustifiedGallery from '../components/JustifiedGallery.vue'
import PurgeBar from '../components/PurgeBar.vue'
import { openLightbox } from '../composables/useLightbox'

const photos = usePhotosStore()
const recipes = useRecipesStore()

const minRating = ref<number>(photos.filter.minRating ?? 0)
const decision = ref<string>(photos.filter.decision ?? '')
const recipeHash = ref<string>(photos.filter.recipe ?? '')

const decisionOptions = [
  { label: 'Toutes décisions', value: '' },
  { label: 'À garder', value: 'keep' },
  { label: 'À jeter', value: 'discard' },
  { label: 'Non décidé', value: 'none' },
]

// Seules les recettes nommées ET appariables (empreinte calculée) filtrent.
const recipeOptions = computed(() => [
  { label: 'Toutes recettes', value: '' },
  ...recipes.list.filter((r) => r.hash).map((r) => ({ label: r.name, value: r.hash })),
])

function applyFilters() {
  photos.setFilter({
    minRating: minRating.value || undefined,
    decision: decision.value || undefined,
    recipe: recipeHash.value || undefined,
  })
}

onMounted(() => {
  if (photos.items.length === 0) void photos.loadMore()
  void recipes.ensure()
})

// Refetch when a finished run invalidates the store (reset → empty) while mounted.
watch(
  () => photos.items.length,
  (n) => {
    if (n === 0 && photos.hasMore && !photos.loading) void photos.loadMore()
  },
)

// Un acteur externe peut poser le filtre pendant que la vue est montée (clic
// sur un nom de recette dans le panneau photo → setFilter + retour galerie) :
// resynchroniser les contrôles locaux, sinon la barre affiche un état périmé
// et le prochain applyFilters() écraserait le filtre posé.
watch(
  () => photos.filter,
  (f) => {
    minRating.value = f.minRating ?? 0
    decision.value = f.decision ?? ''
    recipeHash.value = f.recipe ?? ''
  },
)

function onOpen(index: number) {
  // Both closures are epoch-guarded: after a filter change / refresh the
  // lightbox still shows the old (orphaned) array, so it must neither trigger
  // fetches on the new list nor display the new list's total.
  const epoch = photos.epoch
  openLightbox(photos.items, index, {
    loadMore: () => {
      if (photos.epoch === epoch) void photos.loadMore()
    },
    total: () => (photos.epoch === epoch ? photos.total : 0),
  })
}
</script>

<template>
  <div class="page">
    <NavBar>
      <span class="count">{{ photos.total || photos.items.length }} photos</span>
    </NavBar>

    <div class="filterbar">
      <span class="fb-label">Note min.</span>
      <n-rate
        :value="minRating"
        clearable
        size="small"
        @update:value="(v: number) => { minRating = v || 0; applyFilters() }"
      />
      <n-select
        :value="decision"
        :options="decisionOptions"
        size="small"
        style="width: 170px"
        @update:value="(v: string) => { decision = v; applyFilters() }"
      />
      <n-select
        v-if="recipeOptions.length > 1"
        :value="recipeHash"
        :options="recipeOptions"
        size="small"
        style="width: 200px"
        title="Filtrer par recette"
        @update:value="(v: string) => { recipeHash = v; applyFilters() }"
      />
      <span class="fb-spacer" />
      <n-button size="small" tertiary :loading="photos.loading" title="Rafraîchir la galerie" @click="photos.refresh()">
        <svg class="fb-refresh-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
          <path d="M21 3v5h-5" />
          <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
          <path d="M3 21v-5h5" />
        </svg>
        Rafraîchir
      </n-button>
    </div>

    <!-- Barre de purge : seulement sous le filtre « À jeter », pour que le bouton
         destructeur ne soit jamais visible pendant le tri courant. -->
    <PurgeBar v-if="decision === 'discard'" />

    <JustifiedGallery
      :items="photos.items"
      :has-more="photos.hasMore"
      :loading="photos.loading"
      @open="onOpen"
      @load-more="photos.loadMore()"
    />

    <div v-if="photos.loading" class="status"><n-spin size="small" /> chargement…</div>
    <div v-else-if="photos.error" class="status status--error">{{ photos.error }}</div>
    <div v-else-if="!photos.hasMore && photos.items.length > 0" class="status">— fin —</div>
    <div v-else-if="photos.items.length === 0" class="status">Aucune photo.</div>
  </div>
</template>
