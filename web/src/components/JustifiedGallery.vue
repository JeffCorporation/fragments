<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import justifiedLayout from 'justified-layout'
import type { PhotoItem } from '../api/client'
import CameraMeta from './CameraMeta.vue'
import IconSkull from './IconSkull.vue'
import { formatDate } from '../format'

const props = defineProps<{
  items: PhotoItem[]
  hasMore: boolean
  loading: boolean
}>()

const emit = defineEmits<{
  open: [index: number]
  loadMore: []
}>()

const container = ref<HTMLElement | null>(null)
const sentinel = ref<HTMLElement | null>(null)
const width = ref(0)

let resizeObs: ResizeObserver | null = null
let intersectObs: IntersectionObserver | null = null

// Flickr's justified algorithm: feed it aspect ratios, get back box geometry at
// a fixed row height. Recomputed reactively from items + container width.
const layout = computed(() => {
  if (width.value === 0 || props.items.length === 0) {
    return { boxes: [] as Array<{ width: number; height: number; top: number; left: number }>, containerHeight: 0 }
  }
  return justifiedLayout(
    props.items.map((i) => ({ width: i.width || 1500, height: i.height || 1000 })),
    { containerWidth: width.value, targetRowHeight: 190, boxSpacing: 8, containerPadding: 12 },
  )
})

onMounted(() => {
  resizeObs = new ResizeObserver((entries) => {
    width.value = entries[0].contentRect.width
  })
  if (container.value) resizeObs.observe(container.value)

  intersectObs = new IntersectionObserver(
    (entries) => {
      if (entries[0].isIntersecting && props.hasMore && !props.loading) emit('loadMore')
    },
    { rootMargin: '800px 0px' },
  )
  if (sentinel.value) intersectObs.observe(sentinel.value)
})

// Re-arm the observer after each load: if the appended page didn't push the
// sentinel out of the rootMargin, isIntersecting never toggles and no further
// callback fires (infinite scroll would stall on wide/short layouts). Re-
// observing forces a fresh initial entry for the current intersection state.
watch(
  () => props.loading,
  (now, prev) => {
    if (prev && !now && intersectObs && sentinel.value) {
      intersectObs.unobserve(sentinel.value)
      intersectObs.observe(sentinel.value)
    }
  },
)

onBeforeUnmount(() => {
  resizeObs?.disconnect()
  intersectObs?.disconnect()
})
</script>

<template>
  <div ref="container" class="jg-container" :style="{ height: layout.containerHeight + 'px' }">
    <button
      v-for="(box, i) in layout.boxes"
      :key="props.items[i].keyBase"
      class="jg-tile"
      :style="{ width: box.width + 'px', height: box.height + 'px', top: box.top + 'px', left: box.left + 'px' }"
      @click="emit('open', i)"
    >
      <img
        :src="props.items[i].thumbUrl"
        :alt="props.items[i].name"
        :width="Math.round(box.width)"
        :height="Math.round(box.height)"
        loading="lazy"
        decoding="async"
      />
      <span v-if="props.items[i].decision === 'discard'" class="jg-discard" title="Rejetée">
        <IconSkull />
      </span>
      <span v-if="formatDate(props.items[i].takenAt)" class="jg-date">
        {{ formatDate(props.items[i].takenAt) }}
      </span>
      <div class="jg-overlay">
        <span v-if="props.items[i].rating > 0" class="jg-stars">{{ '★'.repeat(props.items[i].rating) }}</span>
        <span class="jg-overlay-spacer" />
        <CameraMeta :item="props.items[i]" />
      </div>
    </button>
    <div ref="sentinel" class="jg-sentinel" :style="{ top: layout.containerHeight + 'px' }"></div>
  </div>
</template>
