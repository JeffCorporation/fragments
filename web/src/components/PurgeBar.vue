<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NButton, NModal, NSwitch, useMessage } from 'naive-ui'
import { api } from '../api/client'
import type { ApiError, DiscardedSummary } from '../api/client'
import { usePhotosStore } from '../stores/photos'
import { useRunStore } from '../stores/run'
import { formatSize } from '../format'
import IconSkull from './IconSkull.vue'

const photos = usePhotosStore()
const run = useRunStore()
const message = useMessage()

const summary = ref<DiscardedSummary | null>(null)
const showConfirm = ref(false)
const understood = ref(false)
const starting = ref(false)

const count = computed(() => summary.value?.count ?? 0)
const purging = computed(() => run.snap?.phase === 'purging')
const runActive = computed(() => run.snap?.active ?? false)

// Coalesce concurrent triggers (a reset() bumps the epoch AND empties items,
// firing both watchers): one request in flight, at most one queued behind it.
let inflight: Promise<void> | null = null
let queued = false
function refreshSummary(): Promise<void> {
  if (inflight) {
    queued = true
    return inflight
  }
  inflight = (async () => {
    try {
      summary.value = await api.get<DiscardedSummary>('/api/discarded/summary')
    } catch (e) {
      message.error(e instanceof Error ? e.message : 'Échec du décompte des rejets')
    } finally {
      inflight = null
      if (queued) {
        queued = false
        void refreshSummary()
      }
    }
  })()
  return inflight
}

onMounted(() => void refreshSummary())
// Any store reset invalidates the count: end of run, end of purge, filter change.
watch(
  () => photos.epoch,
  () => void refreshSummary(),
)
// Un-rejecting from the lightbox mutates loaded items in place (no reset), so
// react to a DROP in the loaded discard count — growth is just pagination.
watch(
  () => photos.items.filter((i) => i.decision === 'discard').length,
  (n, old) => {
    if (n < old) void refreshSummary()
  },
)

function openConfirm() {
  understood.value = false
  void refreshSummary() // fresh numbers in the dialog
  showConfirm.value = true
}

async function confirmPurge() {
  if (count.value === 0) {
    // openConfirm() refetches: the pile can drain to zero while the dialog sits open.
    showConfirm.value = false
    message.info('Plus aucune photo rejetée — rien à effacer.')
    return
  }
  starting.value = true
  try {
    await run.purgeDiscarded(count.value)
    showConfirm.value = false
  } catch (e) {
    showConfirm.value = false
    const err = e as ApiError
    if (err.status === 409 && err.message.startsWith('discard count changed')) {
      message.warning('Le nombre de photos rejetées a changé depuis l’affichage — rien n’a été effacé.')
      photos.refresh()
    } else if (err.status === 409) {
      message.error('Un traitement est déjà en cours — réessayez quand il sera terminé.')
    } else {
      message.error(e instanceof Error ? e.message : 'Échec du démarrage de la purge')
    }
    void refreshSummary()
  } finally {
    starting.value = false
  }
}
</script>

<template>
  <div class="purgebar">
    <IconSkull class="pb-skull" />
    <span v-if="count > 0" class="pb-text">
      <strong>{{ count }}</strong> photo{{ count > 1 ? 's' : '' }} rejetée{{ count > 1 ? 's' : '' }}
      — {{ formatSize(summary!.bytes) }} récupérables
    </span>
    <span v-else class="pb-text">Aucune photo rejetée</span>
    <span class="fb-spacer" />
    <span v-if="purging" class="pb-progress">
      Purge en cours… {{ (run.snap?.processed ?? 0) + (run.snap?.failed ?? 0) }}/{{ run.snap?.total ?? 0 }}
    </span>
    <n-button
      v-else
      type="error"
      size="small"
      :disabled="count === 0 || runActive"
      :title="runActive ? 'Un traitement est déjà en cours' : undefined"
      @click="openConfirm"
    >
      {{ count > 1 ? `Effacer définitivement les ${count} photos rejetées`
        : count === 1 ? 'Effacer définitivement la photo rejetée'
        : 'Effacer définitivement' }}
    </n-button>
  </div>

  <!-- Safety switch: Échap / clic extérieur annulent (comportement NModal par
       défaut), et le bouton destructeur reste inerte tant que le toggle est off. -->
  <n-modal v-model:show="showConfirm">
    <div class="purge-dialog" role="alertdialog" aria-labelledby="pd-title">
      <div class="pd-header">
        <svg class="pd-danger" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
        <span id="pd-title" class="pd-title">Effacer définitivement les photos rejetées ?</span>
      </div>
      <p>
        Cette action est <strong>irréversible</strong> : elle supprime les
        <strong>originaux dans le bucket</strong>, RAW compris. fragments n’a pas
        de corbeille S3 et ne remet jamais un objet en place.
      </p>
      <ul class="pd-recap">
        <li><strong>{{ count }}</strong> photo{{ count > 1 ? 's' : '' }}</li>
        <li><strong>{{ summary?.objects ?? 0 }}</strong> objet{{ (summary?.objects ?? 0) > 1 ? 's' : '' }} S3 (JPEG + RAW)</li>
        <li><strong>{{ formatSize(summary?.bytes ?? 0) || '0 o' }}</strong></li>
      </ul>
      <p v-if="(summary?.inAlbums ?? 0) > 0" class="pd-albums">
        {{ summary!.inAlbums > 1
          ? `${summary!.inAlbums} de ces photos appartiennent à des albums ; elles en seront retirées.`
          : 'Une de ces photos appartient à un album ; elle en sera retirée.' }}
      </p>
      <label class="pd-switch">
        <n-switch v-model:value="understood" />
        Je comprends que c’est définitif
      </label>
      <div class="pd-actions">
        <n-button tertiary @click="showConfirm = false">Annuler</n-button>
        <n-button type="error" :disabled="!understood || starting || count === 0" :loading="starting" @click="confirmPurge">
          Effacer définitivement {{ count }} photo{{ count > 1 ? 's' : '' }}
        </n-button>
      </div>
    </div>
  </n-modal>
</template>
