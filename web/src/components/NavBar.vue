<script setup lang="ts">
import { useRouter } from 'vue-router'
import { NButton } from 'naive-ui'
import { useAuthStore } from '../stores/auth'

const router = useRouter()
const auth = useAuthStore()

async function logout() {
  await auth.logout()
  void router.push({ name: 'login' })
}
</script>

<template>
  <header class="topbar">
    <span class="brand">fragments</span>
    <nav class="nav">
      <router-link to="/" class="navlink">Galerie</router-link>
      <router-link to="/albums" class="navlink">Albums</router-link>
      <router-link to="/catalog" class="navlink">Catalogue</router-link>
    </nav>
    <span class="topbar-spacer" />
    <slot />
    <n-button quaternary size="small" @click="logout">Déconnexion</n-button>
  </header>
</template>
