<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton, NSpin, useMessage } from 'naive-ui'
import { useAuthStore } from '../stores/auth'

const password = ref('')
const loading = ref(false)
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const message = useMessage()

// Error codes appended by the server's OIDC redirects (see server/oidc.go).
const oidcErrors: Record<string, string> = {
  oidc_unavailable: "Fournisseur d'identité injoignable, réessaie plus tard",
  access_denied: "Accès refusé par le fournisseur d'identité",
  oidc_failed: 'Échec de la connexion OIDC',
}

onMounted(() => {
  void auth.fetchAuthConfig()
  const err = route.query.error
  if (typeof err === 'string' && err) {
    message.error(oidcErrors[err] ?? 'Échec de connexion')
    void router.replace({ query: {} })
  }
})

// Full-page navigation, not an XHR: the flow is a chain of 302s that must set
// cookies and end on the IdP's login page.
function oidcLogin() {
  window.location.href = '/api/auth/oidc/start'
}

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
      <div v-if="auth.mode === null" class="login-loading">
        <n-spin size="small" />
      </div>
      <n-button
        v-else-if="auth.mode === 'oidc'"
        type="primary"
        size="large"
        block
        @click="oidcLogin"
      >
        Se connecter avec {{ auth.providerName }}
      </n-button>
      <n-form v-else @submit.prevent="submit">
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

<style scoped>
.login-loading {
  display: flex;
  justify-content: center;
  padding: 16px 0;
}
</style>
