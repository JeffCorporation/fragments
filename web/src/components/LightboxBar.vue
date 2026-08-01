<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { NDropdown, useMessage } from 'naive-ui'
import type { DropdownOption } from 'naive-ui'
import { lightbox, currentItem, advanceLightbox } from '../composables/useLightbox'
import { usePhotosStore } from '../stores/photos'
import { downloadPhotoPath } from '../api/client'
import StarRating from './StarRating.vue'
import AddToAlbum from './AddToAlbum.vue'
import CameraMeta from './CameraMeta.vue'
import IconSkull from './IconSkull.vue'
import { formatDateTime, formatSize } from '../format'

// Two overlays teleported above PhotoSwipe's UI:
//  - a discreet quick-info strip beside the top-left counter (filename / date /
//    aperture / shutter / ISO), shown only when the width allows;
//  - a slim, icon-only, single-row action bar at the bottom that can't wrap or
//    grow tall and cover the photo.
// The full metadata always lives in the Infos drawer (ExifPanel) and on tiles.
const photos = usePhotosStore()
const message = useMessage()
const item = computed(() => currentItem())
const dateText = computed(() => formatDateTime(item.value?.takenAt ?? null))

// Denominator of the "n / N" counter: the server total when the opener provided
// one (gallery), the loaded length otherwise (albums). Math.max covers a total
// not yet fetched (0), an epoch-stale getter (0), and a server total
// momentarily below the loaded count.
const lightboxTotal = computed(() =>
  Math.max(lightbox.getTotal ? lightbox.getTotal() : 0, lightbox.items.length),
)

async function rate(n: number) {
  if (!item.value) return
  try {
    await photos.setRating(item.value.keyBase, n)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec')
  }
}

// Reject toggle (the only decision now): a rating means "kept", the skull means
// "rejected", neither means undecided. Clicking the skull again clears it.
async function toggleDiscard() {
  if (!item.value) return
  const next = item.value.decision === 'discard' ? 'none' : 'discard'
  try {
    await photos.setDecision(item.value.keyBase, next)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec')
  }
}

// ----- Keyboard culling shortcuts -----
// 1–5 rate, X reject, 0 clears the rating. After a rate/reject we auto-advance
// to the next photo, with a short delay so the star/skull visibly registers
// first. A single shared timer means rapid keypresses on the same photo collapse
// to one advance, and the mutation always targets the on-screen photo.
const ADVANCE_DELAY = 150
let advanceTimer: ReturnType<typeof setTimeout> | null = null
function scheduleAdvance() {
  if (advanceTimer) clearTimeout(advanceTimer)
  advanceTimer = setTimeout(() => {
    advanceTimer = null
    advanceLightbox()
  }, ADVANCE_DELAY)
}

// Any slide change while an auto-advance is pending (arrow key, swipe) makes it
// obsolete — letting it fire would advance a second time and skip a photo
// unseen. The auto-advance itself is safe: the timer is nulled before it runs.
watch(
  () => lightbox.index,
  () => {
    if (advanceTimer) {
      clearTimeout(advanceTimer)
      advanceTimer = null
    }
  },
)

async function rateAndAdvance(n: number) {
  const t = item.value
  if (!t) return
  const done = photos.setRating(t.keyBase, n) // optimistic update applies now
  scheduleAdvance()
  try {
    await done
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec')
  }
}

async function rejectAndAdvance() {
  const t = item.value
  if (!t) return
  const done = photos.setDecision(t.keyBase, 'discard') // always set (not toggle)
  scheduleAdvance()
  try {
    await done
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec')
  }
}

// ----- Spontaneous export -----
// Three fixed entries, each annotated with its real weight. RAW-less photos keep
// the RAW/zip entries VISIBLE but greyed (the menu must not change shape), with a
// native title tooltip — naive-ui merges an option's `props` onto its DOM node,
// which still gets hover events when disabled. Simpler than nesting an NTooltip
// that would need its own z-index above PhotoSwipe.
const NO_RAF_TIP = { title: 'Pas de RAW pour cette photo' }
const downloadOptions = computed<DropdownOption[]>(() => {
  const t = item.value
  if (!t) return []
  return [
    { key: 'jpeg', label: `JPEG — ${formatSize(t.jpegSize)}` },
    {
      key: 'raw',
      label: t.hasRaf ? `RAW — ${formatSize(t.rafSize)}` : 'RAW',
      disabled: !t.hasRaf,
      props: t.hasRaf ? undefined : NO_RAF_TIP,
    },
    {
      key: 'both',
      label: 'Les deux (.zip)',
      disabled: !t.hasRaf,
      props: t.hasRaf ? undefined : NO_RAF_TIP,
    },
  ]
})

// Plain navigation: the session cookie is SameSite=Lax so it rides along, and
// Content-Disposition keeps the page in place (same pattern as the album export).
function onDownload(kind: 'jpeg' | 'raw' | 'both') {
  if (!item.value) return
  window.location.href = downloadPhotoPath(item.value.keyBase, kind)
}

async function clearRating() {
  const t = item.value
  if (!t) return
  try {
    await photos.setRating(t.keyBase, 0)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec')
  }
}

function isTypingTarget(el: EventTarget | null): boolean {
  const node = el as HTMLElement | null
  return !!node && (node.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(node.tagName))
}

function onKeydown(e: KeyboardEvent) {
  if (!lightbox.open) return
  if (e.ctrlKey || e.metaKey || e.altKey) return // keep browser shortcuts
  if (e.repeat) return // one keypress = one photo, even if held
  if (isTypingTarget(e.target)) return
  const k = e.key
  if (k >= '1' && k <= '5') {
    e.preventDefault()
    void rateAndAdvance(Number(k))
  } else if (k === 'x' || k === 'X') {
    e.preventDefault()
    void rejectAndAdvance()
  } else if (k === '0') {
    e.preventDefault()
    void clearRating()
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  if (advanceTimer) clearTimeout(advanceTimer)
})
</script>

<template>
  <Teleport to="body">
    <template v-if="lightbox.open && item">
      <!-- Compteur « n / N » maison : le natif de PhotoSwipe est désactivé
           (counter:false) car il ne sait afficher que le nombre de photos déjà
           chargées ; ici N est le vrai total serveur (filtre compris). -->
      <div class="lb-counter" aria-hidden="true">{{ lightbox.index + 1 }} / {{ lightboxTotal }}</div>

      <!-- Infos rapides à côté du compteur (haut-gauche). Affichées
           seulement quand la largeur le permet ; sinon elles restent dans le
           tiroir Infos. aria-hidden : purement décoratif (doublon du tiroir). -->
      <div class="lb-topinfo" aria-hidden="true">
        <span class="lb-ti-name">{{ item.name }}</span>
        <span v-if="dateText" class="lb-ti-date">{{ dateText }}</span>
        <CameraMeta :item="item" class="lb-ti-exif" />
      </div>

      <div
        class="lightbox-bar"
        :class="{ 'lightbox-bar--shifted': lightbox.detailOpen }"
        role="toolbar"
        aria-label="Actions de la photo"
      >
        <!-- Notation 1–5 : recliquer la note actuelle l'efface (logique dans StarRating). -->
        <StarRating
          class="lb-stars"
          :size="20"
          :model-value="item.rating"
          title="Noter (1–5, 0 pour effacer)"
          @update:model-value="rate"
        />

        <span class="lb-sep" aria-hidden="true"></span>

        <!-- Jeter (bascule) : le crâne, l'icône de rejet de l'app. Rouge quand actif. -->
        <button
          type="button"
          class="lb-btn"
          :class="{ 'is-discard': item.decision === 'discard' }"
          :aria-pressed="item.decision === 'discard'"
          title="Jeter (X)"
          aria-label="Jeter"
          @click="toggleDiscard()"
        >
          <IconSkull class="lb-ico" />
        </button>

        <!-- Ajouter à un album : AddToAlbum fournit le NDropdown ; on lui passe l'icône via son slot. -->
        <AddToAlbum :key-base="item.keyBase">
          <button
            type="button"
            class="lb-btn"
            title="Ajouter à un album"
            aria-label="Ajouter à un album"
            aria-haspopup="menu"
          >
            <svg
              viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
            >
              <path d="M3 7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
              <line x1="12" y1="11" x2="12" y2="16" />
              <line x1="9.5" y1="13.5" x2="14.5" y2="13.5" />
            </svg>
          </button>
        </AddToAlbum>

        <!-- Télécharger : JPEG / RAW / zip des deux, poids réels dans le menu.
             Même z-index que le dropdown d'AddToAlbum, au-dessus de PhotoSwipe. -->
        <n-dropdown trigger="click" :options="downloadOptions" :z-index="100003" @select="onDownload">
          <button
            type="button"
            class="lb-btn"
            title="Télécharger"
            aria-label="Télécharger"
            aria-haspopup="menu"
          >
            <svg
              viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
            >
              <path d="M12 4v11" />
              <path d="M7 10.5 12 15l5-4.5" />
              <line x1="5" y1="20" x2="19" y2="20" />
            </svg>
          </button>
        </n-dropdown>

        <!-- Infos : bascule le tiroir EXIF ; pastille pleine accent quand ouvert. -->
        <button
          type="button"
          class="lb-btn"
          :class="{ 'is-info': lightbox.detailOpen }"
          :aria-pressed="lightbox.detailOpen"
          title="Infos"
          aria-label="Infos"
          @click="lightbox.detailOpen = !lightbox.detailOpen"
        >
          <svg
            viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
          >
            <circle cx="12" cy="12" r="9" />
            <path d="M12 11v5" />
            <path d="M12 8h.01" />
          </svg>
        </button>
      </div>
    </template>
  </Teleport>
</template>
