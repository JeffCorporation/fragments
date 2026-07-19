<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton, useMessage } from 'naive-ui'
import { useAuthStore } from '../stores/auth'

const password = ref('')
const loading = ref(false)
const auth = useAuthStore()
const router = useRouter()
const message = useMessage()

async function submit() {
  if (loading.value) return
  loading.value = true
  try {
    await auth.login(password.value)
    void router.push({ name: 'gallery' })
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de connexion')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <n-card class="login-card" title="fragments">
      <n-form @submit.prevent="submit">
        <n-form-item label="Mot de passe" :show-feedback="false">
          <n-input
            v-model:value="password"
            type="password"
            size="large"
            placeholder="Mot de passe"
            autocomplete="current-password"
            @keyup.enter="submit"
          />
        </n-form-item>
        <n-button
          type="primary"
          size="large"
          block
          attr-type="submit"
          :loading="loading"
          style="margin-top: 16px"
          @click="submit"
        >
          Se connecter
        </n-button>
      </n-form>
    </n-card>
  </div>
</template>
