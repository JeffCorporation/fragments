<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { lightbox, currentItem } from '../composables/useLightbox'
import { loadPhotoDetail } from '../composables/usePhotoDetail'
import type { PhotoDetail } from '../api/client'
import { parseExifJson } from '../exif'
import { formatDateTime } from '../format'

// On-demand detail panel for the photo currently shown in the lightbox. A
// readable "Résumé" is built from the gallery item we already have (so it paints
// instantly); the full EXIF + Fujifilm dumps are fetched lazily from
// /api/photos/{keyBase} the first time the panel is opened for a given photo.
// Toggled via lightbox.detailOpen (the "Infos" button in LightboxBar).

const detail = ref<PhotoDetail | null>(null)
const loading = ref(false)
const error = ref('')

const item = computed(() => currentItem())

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
              <div v-for="r in summary" :key="r.label" class="exif-row">
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
            </section>

            <p v-if="!parsed" class="exif-status">Aucune métadonnée EXIF détaillée.</p>
          </template>
        </div>
      </aside>
    </Transition>
  </Teleport>
</template>
