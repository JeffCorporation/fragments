<script setup lang="ts">
import type { PhotoItem } from '../api/client'

// Discreet essential camera settings with small icons: aperture (ƒ-number),
// shutter speed, ISO. Each is shown only when present in the EXIF.
defineProps<{ item: PhotoItem }>()
</script>

<template>
  <span class="exif">
    <span v-if="item.fNumber" class="exif-i" :title="`Ouverture f/${item.fNumber}`">
      <svg class="exif-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="10" />
        <path d="M14.31 8 20.05 17.94M9.69 8h11.48M7.38 12 13.12 2.06M9.69 16 3.95 6.06M14.31 16H2.83M16.62 12 10.88 21.94" />
      </svg>
      <span>{{ item.fNumber }}</span>
    </span>

    <span v-if="item.exposureTime" class="exif-i" :title="`Vitesse ${item.exposureTime} s`">
      <svg class="exif-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <line x1="10" x2="14" y1="2" y2="2" />
        <line x1="12" x2="15" y1="14" y2="11" />
        <circle cx="12" cy="14" r="8" />
      </svg>
      <span>{{ item.exposureTime }}</span>
    </span>

    <span v-if="item.iso" class="exif-i" :title="`ISO ${item.iso}`">
      <svg class="exif-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M3.34 19a10 10 0 1 1 17.32 0" />
        <path d="m12 14 4-4" />
      </svg>
      <span>{{ item.iso }}</span>
    </span>
  </span>
</template>
