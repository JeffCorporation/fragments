<script setup lang="ts">
import { watch } from 'vue'
import { useMessage } from 'naive-ui'
import { useRunStore } from '../stores/run'
import { formatSize } from '../format'

// Renders nothing: this component only watches the SSE snapshot and toasts the
// outcome of a purge. It lives in App.vue (always mounted, under
// NMessageProvider — useMessage requires it) so the toast survives leaving the
// « À jeter » filter or the gallery while the purge runs.
const run = useRunStore()
const message = useMessage()

watch(
  () => run.snap?.phase,
  (phase, prev) => {
    if (prev !== 'purging') return
    const s = run.snap
    if (!s) return
    if (phase === 'done') {
      const spared = s.skipped > 0 ? `, ${s.skipped} épargnée${s.skipped > 1 ? 's' : ''}` : ''
      const base = `${s.processed} photo${s.processed > 1 ? 's' : ''} effacée${s.processed > 1 ? 's' : ''}, ${formatSize(s.bytesFreed) || '0 o'} libérés${spared}`
      if (s.failed > 0) message.warning(`${base} — ${s.failed} échec${s.failed > 1 ? 's' : ''}, les photos concernées restent rejetées`)
      else message.success(base)
    } else if (phase === 'cancelled') {
      message.info('Purge annulée — les photos restantes sont toujours rejetées')
    } else if (phase === 'error') {
      message.error(`Purge en erreur : ${s.lastError || 'erreur inconnue'}`)
    }
  },
)
</script>

<template></template>
