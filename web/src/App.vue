<script setup lang="ts">
import { NConfigProvider, NMessageProvider, NGlobalStyle, darkTheme } from 'naive-ui'
import type { GlobalThemeOverrides } from 'naive-ui'
import LightboxBar from './components/LightboxBar.vue'
import ExifPanel from './components/ExifPanel.vue'

// Single source of theme truth: a dark Naive theme with an amber accent that
// matches the bespoke gallery CSS. Dark-by-default suits the Steam Deck (glare +
// OLED battery) and makes thumbnails pop.
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#63e2b7',
    primaryColorHover: '#7fe7c4',
    primaryColorPressed: '#5acea7',
    primaryColorSuppl: '#2a947d',
    bodyColor: '#0a0a0a',
  },
}
</script>

<template>
  <n-config-provider :theme="darkTheme" :theme-overrides="themeOverrides">
    <n-global-style />
    <n-message-provider>
      <router-view />
      <LightboxBar />
      <ExifPanel />
    </n-message-provider>
  </n-config-provider>
</template>
