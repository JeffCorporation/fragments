import { defineStore } from 'pinia'
import { api, setCsrf } from '../api/client'
import { useRunStore } from './run'

interface AuthState {
  authenticated: boolean
  checked: boolean
  csrf: string
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({ authenticated: false, checked: false, csrf: '' }),
  actions: {
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
