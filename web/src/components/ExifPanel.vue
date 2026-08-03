<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, useMessage } from 'naive-ui'
import { lightbox, currentItem, closeLightbox } from '../composables/useLightbox'
import { loadPhotoDetail } from '../composables/usePhotoDetail'
import type { PhotoDetail, Recipe, RecipeFields } from '../api/client'
import { usePhotosStore } from '../stores/photos'
import { parseExifJson } from '../exif'
import { formatDateTime } from '../format'
import RecipeEditor from './RecipeEditor.vue'

// On-demand detail panel for the photo currently shown in the lightbox. A
// readable "Résumé" is built from the gallery item we already have (so it paints
// instantly); the full EXIF + Fujifilm dumps are fetched lazily from
// /api/photos/{keyBase} the first time the panel is opened for a given photo.
// Toggled via lightbox.detailOpen (the "Infos" button in LightboxBar).

const detail = ref<PhotoDetail | null>(null)
const loading = ref(false)
const error = ref('')

const item = computed(() => currentItem())

const router = useRouter()
const photosStore = usePhotosStore()
const message = useMessage()
const showNameEditor = ref(false)
// Préremplissage figé au moment du clic : ouvrir l'éditeur ferme la
// visionneuse, ce qui démonte le panneau et vide `detail` — le computed
// recipePrefill serait déjà null quand l'éditeur s'initialise.
const nameEditorPrefill = ref<RecipeFields | null>(null)

// A monotonic token guards against stale responses: swiping A→B while A's fetch
// is still in flight must not let A's (late) response overwrite B's detail. Any
// superseded load returns without touching the shared refs.
let reqToken = 0
async function load(keyBase: string) {
  const token = ++reqToken
  loading.value = true
  error.value = ''
  try {
    const d = await loadPhotoDetail(keyBase)
    if (token !== reqToken) return
    detail.value = d
  } catch (e) {
    if (token !== reqToken) return
    detail.value = null
    error.value = e instanceof Error ? e.message : 'Échec du chargement des métadonnées'
  } finally {
    if (token === reqToken) loading.value = false
  }
}

// Fetch (or refetch when swiping to another photo) only while the panel is open.
watch(
  () => (lightbox.open && lightbox.detailOpen ? item.value?.keyBase ?? '' : ''),
  (keyBase) => {
    detail.value = null
    if (keyBase) void load(keyBase)
  },
  { immediate: true },
)

const parsed = computed(() => (detail.value ? parseExifJson(detail.value.exifJson) : null))

interface Row {
  label: string
  value: string
}

const summary = computed<Row[]>(() => {
  const it = item.value
  if (!it) return []
  const rows: Row[] = []
  const push = (label: string, value: string) => {
    const v = value.trim()
    if (v) rows.push({ label, value: v })
  }
  push('Fichier', it.name)
  push('Dossier', it.folder)
  push('Date', formatDateTime(it.takenAt))
  push('Appareil', it.cameraModel)
  push('Objectif', it.lensModel)
  if (it.focalLength) push('Focale', `${formatNum(it.focalLength)} mm`)
  if (it.fNumber) push('Ouverture', `f/${formatNum(it.fNumber)}`)
  if (it.exposureTime) push('Vitesse', shutter(it.exposureTime))
  if (it.iso) push('ISO', String(it.iso))
  push('Simulation', it.filmSimulation)
  if (it.width && it.height) {
    push('Définition', `${it.width} × ${it.height} (${megapixels(it.width, it.height)} Mpx)`)
  }
  if (detail.value) push('RAW', detail.value.rafKey ? 'Disponible' : 'Non')
  return rows
})

// La ligne Recette s'insère sous la ligne Simulation : le Résumé est donc
// coupé en deux autour d'elle (la recette n'arrive qu'avec le détail chargé).
const summarySplit = computed(() => {
  const rows = summary.value
  const i = rows.findIndex((r) => r.label === 'Simulation')
  return i < 0 ? { head: rows, tail: [] as Row[] } : { head: rows.slice(0, i + 1), tail: rows.slice(i + 1) }
})

// Champs canoniques décodés de la photo (fuji.Recipe du dump EXIF), pour
// pré-remplir l'éditeur du bouton « Nommer cette recette ».
const recipePrefill = computed<RecipeFields | null>(() => {
  const d = detail.value
  if (!d || !d.recipeHash) return null
  try {
    const obj = JSON.parse(d.exifJson) as { fuji?: { Recipe?: unknown } }
    const rec = obj?.fuji?.Recipe
    return rec && typeof rec === 'object' ? (rec as RecipeFields) : null
  } catch {
    return null
  }
})

// Nom cliquable → galerie filtrée sur cette recette (la lightbox se ferme).
function openRecipeGallery() {
  const d = detail.value
  if (!d?.recipeHash) return
  photosStore.setFilter({ recipe: d.recipeHash })
  closeLightbox()
  void router.push({ name: 'gallery' })
}

// L'éditeur modal (naive-ui) vit très en dessous du z-index de PhotoSwipe
// (100000) : ouvert par-dessus la visionneuse, il serait caché par la photo.
// On ferme donc la visionneuse et l'éditeur s'affiche seul, préremplissage
// capturé avant que la fermeture ne vide `detail`.
function openNameEditor() {
  nameEditorPrefill.value = recipePrefill.value
  closeLightbox()
  showNameEditor.value = true
}

// Après enregistrement : la visionneuse est fermée, donc pas de ligne à
// rafraîchir sous les yeux de l'utilisateur — un toast confirme, et le cache
// photo-détail (purgé par le store recettes) refera foi à la prochaine
// ouverture. Si le panneau était encore ouvert (garde ci-dessous), la ligne
// passe du bouton au nom sans refetch.
function onRecipeSaved(r: Recipe) {
  const d = detail.value
  if (d && r.hash && r.hash === d.recipeHash) {
    d.recipeName = r.name
    d.recipeId = r.id
  }
  message.success(`Recette « ${r.name} » enregistrée`)
}

const gps = computed(() => {
  const d = detail.value
  if (!d || d.gpsLat == null || d.gpsLon == null) return null
  const { gpsLat: lat, gpsLon: lon } = d
  return {
    text: `${lat.toFixed(5)}, ${lon.toFixed(5)}`,
    url: `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}#map=15/${lat}/${lon}`,
  }
})

function close() {
  lightbox.detailOpen = false
}

// Trim trailing zeros: 23 -> "23", 2.0 -> "2", 1.40 -> "1.4".
function formatNum(n: number): string {
  return Number(n.toFixed(2)).toString()
}

function megapixels(w: number, h: number): string {
  return ((w * h) / 1e6).toFixed(1)
}

// The backend renders sub-second speeds as a fraction ("1/250") and longer ones
// with a trailing "s" ("2s"); only the fraction form needs a " s" appended.
function shutter(s: string): string {
  return /\//.test(s) ? `${s} s` : s
}
</script>

<template>
  <Teleport to="body">
    <Transition name="exif-slide">
      <aside
        v-if="lightbox.open && lightbox.detailOpen && item"
        class="exif-panel"
        role="region"
        aria-label="Détails de la photo"
      >
        <header class="exif-panel-head">
          <span class="exif-panel-title" :title="item.keyBase">{{ item.name }}</span>
          <button class="exif-panel-close" aria-label="Fermer les détails" @click="close">×</button>
        </header>

        <div class="exif-panel-body">
          <section class="exif-section">
            <h3 class="exif-section-title">Résumé</h3>
            <dl class="exif-dl">
              <div v-for="r in summarySplit.head" :key="r.label" class="exif-row">
                <dt>{{ r.label }}</dt>
                <dd>{{ r.value }}</dd>
              </div>
              <!-- Recette : nom cliquable si nommée, bouton pour la baptiser
                   sinon ; pas de ligne du tout sans données Fujifilm. -->
              <div v-if="detail && detail.recipeHash" class="exif-row">
                <dt>Recette</dt>
                <dd>
                  <a v-if="detail.recipeName" href="#" :title="'Voir les photos de « ' + detail.recipeName + ' »'"
                     @click.prevent="openRecipeGallery">{{ detail.recipeName }}</a>
                  <n-button v-else size="tiny" tertiary @click="openNameEditor">
                    Nommer cette recette
                  </n-button>
                </dd>
              </div>
              <div v-for="r in summarySplit.tail" :key="r.label" class="exif-row">
                <dt>{{ r.label }}</dt>
                <dd>{{ r.value }}</dd>
              </div>
              <div v-if="gps" class="exif-row">
                <dt>GPS</dt>
                <dd>
                  <a :href="gps.url" target="_blank" rel="noopener noreferrer">{{ gps.text }}</a>
                </dd>
              </div>
            </dl>
          </section>

          <p v-if="loading" class="exif-status">Chargement des métadonnées…</p>
          <p v-else-if="error" class="exif-status exif-status--error">{{ error }}</p>
          <template v-else>
            <section v-if="parsed && parsed.exif.length" class="exif-section">
              <h3 class="exif-section-title">EXIF</h3>
              <dl class="exif-dl">
                <div v-for="e in parsed.exif" :key="e.key" class="exif-row">
                  <dt>{{ e.label }}</dt>
                  <dd>{{ e.value }}</dd>
                </div>
              </dl>
            </section>

            <section v-if="parsed && parsed.fuji.length" class="exif-section">
              <h3 class="exif-section-title">Fujifilm</h3>
              <dl class="exif-dl">
                <div v-for="e in parsed.fuji" :key="e.key" class="exif-row">
                  <dt>{{ e.label }}</dt>
                  <dd>{{ e.value }}</dd>
                </div>
              </dl>
              <!-- Empreinte encore anonyme : proposer de créer la recette
                   directement sous les réglages qu'on est en train de lire. -->
              <n-button
                v-if="detail && detail.recipeHash && !detail.recipeName"
                size="tiny"
                tertiary
                class="fuji-recipe-btn"
                @click="openNameEditor"
              >
                Créer une recette avec ces réglages
              </n-button>
            </section>

            <p v-if="!parsed" class="exif-status">Aucune métadonnée EXIF détaillée.</p>
          </template>
        </div>
      </aside>
    </Transition>
    <RecipeEditor v-model:show="showNameEditor" :prefill="nameEditorPrefill" @saved="onRecipeSaved" />
  </Teleport>
</template>
