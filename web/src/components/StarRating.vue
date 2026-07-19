<script setup lang="ts">
// A 1–5 star control. Clicking a star sets that rating; clicking the current
// rating again clears it (0). Tap targets are sized for the Steam Deck.
const props = defineProps<{ modelValue: number; size?: number }>()
const emit = defineEmits<{ 'update:modelValue': [n: number] }>()

function pick(n: number) {
  emit('update:modelValue', props.modelValue === n ? 0 : n)
}
</script>

<template>
  <span class="stars" :style="{ fontSize: (size ?? 22) + 'px' }">
    <button
      v-for="n in 5"
      :key="n"
      type="button"
      class="star"
      :class="{ on: n <= modelValue }"
      :aria-label="`${n} étoile${n > 1 ? 's' : ''}`"
      @click.stop="pick(n)"
    >
      ★
    </button>
  </span>
</template>
