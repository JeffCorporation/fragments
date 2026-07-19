import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'
import { useRunStore } from './stores/run'
import { setCsrf, setUnauthorizedHandler } from './api/client'
import GalleryView from './views/GalleryView.vue'
import CatalogView from './views/CatalogView.vue'
import AlbumsView from './views/AlbumsView.vue'
import AlbumDetailView from './views/AlbumDetailView.vue'
import LoginView from './views/LoginView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView },
    { path: '/', name: 'gallery', component: GalleryView, meta: { requiresAuth: true } },
    { path: '/albums', name: 'albums', component: AlbumsView, meta: { requiresAuth: true } },
    { path: '/albums/:id', name: 'album', component: AlbumDetailView, meta: { requiresAuth: true } },
    { path: '/catalog', name: 'catalog', component: CatalogView, meta: { requiresAuth: true } },
  ],
})

// A 401 on any authenticated request (session expired mid-browse) resets auth
// state and bounces to login. Runs at request time, when Pinia is active.
setUnauthorizedHandler(() => {
  const auth = useAuthStore()
  auth.authenticated = false
  auth.csrf = ''
  setCsrf('')
  // Tear down the SSE stream too: its own 401 kills it without notice, and a
  // stale instance would block reconnection after the next login.
  useRunStore().disconnect()
  if (router.currentRoute.value.name !== 'login') void router.push({ name: 'login' })
})

// Resolve the session once (GET /api/me) before the first protected navigation.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.checked) await auth.check()
  if (to.meta.requiresAuth && !auth.authenticated) return { name: 'login' }
  if (to.name === 'login' && auth.authenticated) return { name: 'gallery' }
  // Keep one live status stream open across authenticated views (idempotent).
  if (auth.authenticated) useRunStore().connect()
  return true
})

export default router
