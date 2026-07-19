<script setup lang="ts">
import { onMounted, ref, computed, watch } from 'vue'
import {
  NButton, NCard, NProgress, NInputNumber, NSwitch,
  NGrid, NGi, NStatistic, NTag, NSpace, useMessage,
} from 'naive-ui'
import NavBar from '../components/NavBar.vue'
import { useRunStore } from '../stores/run'
import { encodeKeyBase } from '../api/client'

const run = useRunStore()
const message = useMessage()

// Prefix and limit were dropped from the UI on purpose: cataloging always
// processes the whole bucket. They remain available on the CLI (fragments scan).
const force = ref(false)
const workers = ref<number | null>(null)

const snap = computed(() => run.snap)
const active = computed(() => run.snap?.active ?? false)

// Pre-fill the workers input with the server's effective default the first time
// a snapshot arrives (unless the user already typed a value).
watch(
  () => run.snap?.defaultWorkers,
  (d) => {
    if (d && workers.value == null) workers.value = d
  },
  { immediate: true },
)

const phaseLabels: Record<string, string> = {
  idle: 'Au repos',
  listing: 'Listing…',
  running: 'En cours',
  done: 'Terminé',
  cancelled: 'Annulé',
  error: 'Erreur',
}

async function start() {
  try {
    await run.start({
      force: force.value || undefined,
      workers: workers.value || undefined,
    })
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec du démarrage')
  }
}

async function cancel() {
  try {
    await run.cancel()
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de l’annulation')
  }
}

function fmtDuration(sec: number): string {
  if (!sec || sec < 0) return '—'
  const s = Math.round(sec)
  const m = Math.floor(s / 60)
  return m > 0 ? `${m}m ${s % 60}s` : `${s}s`
}

// A worker's last-processed thumbnail can be momentarily missing (or absent if
// that item failed before the thumbnail was written); toggle visibility on
// load/error so a broken image shows the empty slot instead of an icon.
function onThumbLoad(e: Event) {
  const img = e.target as HTMLImageElement
  img.style.visibility = 'visible'
}
function onThumbError(e: Event) {
  const img = e.target as HTMLImageElement
  img.style.visibility = 'hidden'
}

onMounted(() => run.connect())
</script>

<template>
  <div class="page">
    <NavBar />
    <div class="content">
      <n-card title="Catalogage" size="small" class="card">
        <n-space vertical :size="12">
          <n-space align="center" :wrap="true">
            <span class="fb-label">Workers</span>
            <n-input-number v-model:value="workers" :min="1" :max="32" :disabled="active" style="width: 120px" />
            <span class="inline-switch"><n-switch v-model:value="force" :disabled="active" /> forcer</span>
            <n-button type="primary" :disabled="active" @click="start">Démarrer</n-button>
            <n-button type="error" tertiary :disabled="!active" @click="cancel">Annuler</n-button>
          </n-space>
          <n-space align="center" :size="8">
            <n-tag :type="active ? 'warning' : 'default'" size="small" round>
              {{ phaseLabels[snap?.phase ?? 'idle'] ?? snap?.phase }}
            </n-tag>
            <n-tag v-if="!run.connected" type="error" size="small" round>flux déconnecté</n-tag>
          </n-space>
        </n-space>
      </n-card>

      <n-card v-if="snap && snap.total > 0" size="small" class="card">
        <n-progress type="line" :percentage="run.progress" :processing="active" indicator-placement="inside" />
        <n-grid :cols="4" :x-gap="12" style="margin-top: 16px">
          <n-gi><n-statistic label="Traitées" :value="snap.processed" /></n-gi>
          <n-gi><n-statistic label="Ignorées" :value="snap.skipped" /></n-gi>
          <n-gi><n-statistic label="Échecs" :value="snap.failed" /></n-gi>
          <n-gi><n-statistic label="Total" :value="snap.total" /></n-gi>
        </n-grid>
        <n-grid :cols="3" :x-gap="12" style="margin-top: 12px">
          <n-gi><n-statistic label="Écoulé" :value="fmtDuration(snap.elapsedSec)" /></n-gi>
          <n-gi><n-statistic label="Débit" :value="snap.rate.toFixed(1) + ' /s'" /></n-gi>
          <n-gi><n-statistic label="ETA" :value="fmtDuration(snap.etaSec)" /></n-gi>
        </n-grid>
      </n-card>

      <n-card v-if="snap && snap.workers?.length" title="Workers" size="small" class="card">
        <div class="workers">
          <div v-for="w in snap.workers" :key="w.id" class="worker" :class="{ busy: w.busy }">
            <div class="worker-thumb">
              <img
                v-if="w.lastKeyBase"
                :src="`/thumbs/${encodeKeyBase(w.lastKeyBase)}.jpg`"
                :alt="w.lastKeyBase"
                @load="onThumbLoad"
                @error="onThumbError"
              />
              <span v-else class="worker-thumb--empty">—</span>
            </div>
            <div class="worker-meta">
              <span class="wid">#{{ w.id }}</span>
              <span class="wkey">{{ w.busy ? w.keyBase : (w.lastKeyBase || '—') }}</span>
            </div>
          </div>
        </div>
      </n-card>

      <n-card v-if="snap && snap.errors?.length" title="Erreurs récentes" size="small" class="card">
        <ul class="errors">
          <li v-for="(e, i) in snap.errors" :key="i"><code>{{ e.keyBase }}</code> — {{ e.err }}</li>
        </ul>
      </n-card>
    </div>
  </div>
</template>
