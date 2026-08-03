<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, useMessage } from 'naive-ui'
import NavBar from '../components/NavBar.vue'
import RecipeEditor from '../components/RecipeEditor.vue'
import { api } from '../api/client'
import type { PhotoItem, PhotoPage, Recipe } from '../api/client'
import { useRecipesStore } from '../stores/recipes'
import { usePhotosStore } from '../stores/photos'
import { openLightbox } from '../composables/useLightbox'

// Fiche de recette : tous les paramètres en libellés, crédit, notes, photos
// appariées (celles dont l'empreinte est identique), Modifier / Supprimer.

const route = useRoute()
const router = useRouter()
const recipes = useRecipesStore()
const photosStore = usePhotosStore()
const message = useMessage()

const recipeId = computed(() => Number(route.params.id))
const recipe = ref<Recipe | null>(null)
const loading = ref(true)
const showEditor = ref(false)

// Photos appariées : liste locale paginée (indépendante du store galerie).
const photos = ref<PhotoItem[]>([])
const photosCursor = ref('')
const photosTotal = ref(0)
const photosLoading = ref(false)

async function load() {
  loading.value = true
  try {
    recipe.value = await api.get<Recipe>(`/api/recipes/${recipeId.value}`)
    photos.value = []
    photosCursor.value = ''
    photosTotal.value = 0
    if (recipe.value.hash) await loadPhotos()
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec du chargement')
  } finally {
    loading.value = false
  }
}

async function loadPhotos() {
  const r = recipe.value
  if (!r?.hash || photosLoading.value) return
  photosLoading.value = true
  try {
    const q = new URLSearchParams({ recipe: r.hash, limit: '80' })
    if (photosCursor.value) q.set('cursor', photosCursor.value)
    const page = await api.get<PhotoPage>('/api/photos?' + q.toString())
    photos.value.push(...page.items)
    photosCursor.value = page.nextCursor
    photosTotal.value = page.total
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec du chargement des photos')
  } finally {
    photosLoading.value = false
  }
}

onMounted(load)
// vue-router réutilise l'instance quand seul :id change : recharger.
watch(recipeId, () => void load())

function openPhoto(index: number) {
  openLightbox(photos.value, index, {
    loadMore: () => {
      if (photosCursor.value) void loadPhotos()
    },
    total: () => photosTotal.value,
  })
}

function seeInGallery() {
  if (!recipe.value?.hash) return
  photosStore.setFilter({ recipe: recipe.value.hash })
  void router.push({ name: 'gallery' })
}

async function removeRecipe() {
  const r = recipe.value
  if (!r) return
  if (!window.confirm(`Supprimer la recette « ${r.name} » ? (les photos gardent leur empreinte, elles redeviennent simplement anonymes)`)) return
  try {
    await recipes.remove(r.id)
    void router.push({ name: 'recipes' })
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de la suppression')
  }
}

function onSaved(r: Recipe) {
  recipe.value = r
  void load()
}

// Libellés façon menus du boîtier : signe explicite, zéro nu.
function signed(v: number | undefined): string {
  if (v === undefined) return '—'
  const s = String(v)
  return v > 0 ? '+' + s : s
}

interface ParamRow {
  label: string
  value: string
}

const params = computed<ParamRow[]>(() => {
  const r = recipe.value
  if (!r) return []
  const f = r.fields
  const rows: ParamRow[] = []
  const push = (label: string, value: string | undefined) => rows.push({ label, value: value ?? '—' })
  const mono = !!f.filmSimulation && (recipes.schema?.monochromeSimulations.includes(f.filmSimulation)
    ?? /^(Acros|Monochrome|Sepia)/.test(f.filmSimulation))

  push('Simulation de film', f.filmSimulation)
  push('Plage dynamique', f.dynamicRange)
  push('Priorité D-Range', f.dRangePriority)
  push('Hautes lumières', f.highlightTone === undefined ? undefined : signed(f.highlightTone))
  push('Ombres', f.shadowTone === undefined ? undefined : signed(f.shadowTone))
  if (!mono) push('Couleur', f.color === undefined ? undefined : signed(f.color))
  push('Netteté', f.sharpness === undefined ? undefined : signed(f.sharpness))
  push('Réduction du bruit', f.noiseReduction === undefined ? undefined : signed(f.noiseReduction))
  push('Clarté', f.clarity === undefined ? undefined : signed(f.clarity))
  push('Grain', f.grainEffect === 'Off' || !f.grainEffect ? f.grainEffect : `${f.grainEffect}, ${f.grainSize ?? '—'}`)
  push('Color Chrome', f.colorChrome)
  push('Color Chrome FX Blue', f.colorChromeFXBlue)
  push('Balance des blancs', f.whiteBalance === 'Kelvin' && f.colorTemperature
    ? `Kelvin · ${f.colorTemperature} K`
    : f.whiteBalance)
  push('Décalage BB', f.wbShiftRed === undefined || f.wbShiftBlue === undefined
    ? undefined
    : `R${signed(f.wbShiftRed)} / B${signed(f.wbShiftBlue)}`)
  if (mono) {
    push('Couleur monochromatique', `WC${signed(f.monochromaticWC ?? 0)} / MG${signed(f.monochromaticMG ?? 0)}`)
  }
  return rows
})

onMounted(() => void recipes.ensureSchema().catch(() => undefined))
</script>

<template>
  <div class="page">
    <NavBar>
      <span v-if="recipe" class="count">{{ recipe.name }} · {{ photosTotal }} photo{{ photosTotal > 1 ? 's' : '' }}</span>
    </NavBar>
    <div class="content">
      <div v-if="loading" class="status">chargement…</div>
      <template v-else-if="recipe">
        <div class="recipe-head">
          <h2 class="recipe-title">
            {{ recipe.name }}
            <span v-if="recipe.incomplete" class="recipe-badge" title="Champs manquants : empreinte non appariable">incomplète</span>
          </h2>
          <span class="fb-spacer" />
          <n-button v-if="recipe.hash && photosTotal > 0" size="small" tertiary @click="seeInGallery">
            Voir dans la galerie
          </n-button>
          <n-button size="small" tertiary @click="showEditor = true">Modifier</n-button>
          <n-button size="small" tertiary type="error" @click="removeRecipe">Supprimer</n-button>
        </div>

        <!-- Crédit : absent = recette maison, on n'affiche pas de « Auteur : — ». -->
        <p v-if="recipe.author || recipe.source" class="recipe-credit">
          D’après
          <a v-if="recipe.authorUrl" :href="recipe.authorUrl" target="_blank" rel="noopener noreferrer">
            {{ recipe.author || recipe.source }}</a>
          <template v-else>{{ recipe.author || recipe.source }}</template>
          <template v-if="recipe.author && recipe.source"> — {{ recipe.source }}</template>
        </p>

        <div v-if="recipe.incomplete" class="recipe-missing">
          Recette incomplète, non appariée aux photos. Champs manquants :
          {{ (recipe.missingFields ?? []).join(', ') }} — « Modifier » les pré-remplit avec les
          valeurs par défaut du boîtier.
        </div>

        <dl class="recipe-params">
          <div v-for="p in params" :key="p.label" class="exif-row">
            <dt>{{ p.label }}</dt>
            <dd>{{ p.value }}</dd>
          </div>
        </dl>

        <p v-if="recipe.notes" class="recipe-notes">{{ recipe.notes }}</p>

        <h3 class="recipe-photos-title">
          Photos appariées
          <span v-if="photosTotal > photos.length"> ({{ photos.length }} / {{ photosTotal }})</span>
        </h3>
        <div v-if="!recipe.hash" class="status">Sans empreinte, aucune photo ne peut correspondre.</div>
        <div v-else-if="photos.length === 0 && !photosLoading" class="status">
          Aucune photo cataloguée n’utilise ces réglages (pour l’instant).
        </div>
        <div v-else class="recipe-photos">
          <div v-for="(p, i) in photos" :key="p.keyBase" class="rp-tile">
            <img :src="p.thumbUrl" :alt="p.name" loading="lazy" @click="openPhoto(i)" />
          </div>
        </div>
        <div v-if="photosCursor" class="recipe-more">
          <n-button size="small" tertiary :loading="photosLoading" @click="loadPhotos">Charger plus</n-button>
        </div>

        <RecipeEditor v-model:show="showEditor" :recipe="recipe" @saved="onSaved" />
      </template>
      <div v-else class="status">Recette introuvable.</div>
    </div>
  </div>
</template>
