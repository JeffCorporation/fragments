import { defineStore } from 'pinia'
import { api, setCsrf } from '../api/client'
import { useRunStore } from './run'

interface AuthState {
  authenticated: boolean
  checked: boolean
  csrf: string
  // Auth method exposed by the server: password form or OIDC redirect button.
  // null until /api/auth/config has answered (the login view shows a spinner).
  mode: 'password' | 'oidc' | null
  // True only once the server actually answered; a fallback mode set after a
  // network failure is NOT cached, so the next login-view mount retries.
  modeKnown: boolean
  providerName: string
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    authenticated: false,
    checked: false,
    csrf: '',
    mode: null,
    modeKnown: false,
    providerName: 'OIDC',
  }),
  actions: {
    async fetchAuthConfig() {
      if (this.modeKnown) return
      try {
        const r = await api.get<{ mode: 'password' | 'oidc'; providerName?: string }>(
          '/api/auth/config',
        )
        this.mode = r.mode
        this.modeKnown = true
        if (r.providerName) this.providerName = r.providerName
      } catch {
        // Unreachable/older server: default to the password form so login
        // stays possible (uncached — retried on the next mount).
        this.mode = 'password'
      }
    },
    async check() {
      try {
        const r = await api.get<{ csrf: string }>('/api/me')
        this.csrf = r.csrf
        setCsrf(r.csrf)
        this.authenticated = true
      } catch {
        this.authenticated = false
      }
      this.checked = true
    },
    async login(password: string) {
      const r = await api.post<{ csrf: string }>('/api/login', { password })
      this.csrf = r.csrf
      setCsrf(r.csrf)
      this.authenticated = true
    },
    async logout() {
      useRunStore().disconnect()
      try {
        await api.post('/api/logout')
      } catch {
        // ignore — clearing local state is enough
      }
      this.authenticated = false
      this.csrf = ''
      setCsrf('')
    },
  },
})
