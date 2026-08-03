<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NModal, useMessage } from 'naive-ui'
import NavBar from '../components/NavBar.vue'
import RecipeEditor from '../components/RecipeEditor.vue'
import { useRecipesStore } from '../stores/recipes'
import type { Recipe, RecipeImportReport } from '../api/client'

const recipes = useRecipesStore()
const router = useRouter()
const message = useMessage()

const showEditor = ref(false)
const importing = ref(false)
const report = ref<RecipeImportReport | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

onMounted(() => void recipes.fetch())

function open(id: number) {
  void router.push({ name: 'recipe', params: { id } })
}

function onSaved(r: Recipe) {
  open(r.id)
}

function exportAll() {
  // GET même-origine : le cookie de session part avec la navigation et le
  // Content-Disposition déclenche le téléchargement sans quitter la page.
  window.location.href = '/api/recipes/export'
}

async function onFilePicked(ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // permet de réimporter le même fichier
  if (!file) return
  importing.value = true
  try {
    const text = await file.text()
    let entries: unknown
    try {
      entries = JSON.parse(text)
    } catch {
      message.error('Fichier illisible : JSON attendu')
      return
    }
    if (!Array.isArray(entries)) {
      message.error('Fichier invalide : un tableau JSON de recettes est attendu')
      return
    }
    report.value = await recipes.importFile(entries)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de l’import')
  } finally {
    importing.value = false
  }
}

const statusLabels: Record<string, string> = {
  imported: 'importée',
  incomplete: 'incomplète',
  skipped: 'ignorée',
  error: 'erreur',
}

// « source » sert de regroupement lâche : une section par source dès qu'il en
// existe au moins deux distinctes (sinon grille plate, comme les albums). Les
// recettes sans source ferment la marche. L'ordre alphabétique du serveur est
// préservé à l'intérieur de chaque groupe.
const groups = computed(() => {
  const map = new Map<string, Recipe[]>()
  for (const r of recipes.list) {
    const k = (r.source ?? '').trim()
    const g = map.get(k)
    if (g) g.push(r)
    else map.set(k, [r])
  }
  const named = [...map.entries()]
    .filter(([k]) => k !== '')
    .sort((a, b) => a[0].localeCompare(b[0], 'fr'))
    .map(([source, list]) => ({ source, list }))
  const bare = map.get('') ?? []
  if (bare.length > 0) named.push({ source: '', list: bare })
  return { sections: named, sectioned: map.size > 1 }
})
</script>

<template>
  <div class="page">
    <NavBar>
      <n-button size="small" tertiary :loading="importing" @click="fileInput?.click()">Importer…</n-button>
      <n-button size="small" tertiary :disabled="recipes.list.length === 0" @click="exportAll">Exporter</n-button>
      <n-button size="small" type="primary" @click="showEditor = true">+ Nouvelle recette</n-button>
    </NavBar>
    <input ref="fileInput" type="file" accept=".json,application/json" class="visually-hidden" @change="onFilePicked" />

    <div class="content">
      <div v-if="recipes.loaded && recipes.list.length === 0" class="status">
        Aucune recette. Nomme une recette depuis le panneau d’une photo, crée-la ici,
        ou importe un fichier de recettes.
      </div>
      <template v-for="section in groups.sections" :key="section.source || '(sans source)'">
        <h3 v-if="groups.sectioned" class="recipe-source-title">
          {{ section.source || 'Sans source' }}
        </h3>
        <div class="album-grid">
          <div v-for="r in section.list" :key="r.id" class="album-card" @click="open(r.id)">
            <div class="album-cover">
              <img v-if="r.coverThumbUrl" :src="r.coverThumbUrl" :alt="r.name" loading="lazy" />
              <div v-else class="album-cover--empty">{{ r.incomplete ? 'incomplète' : 'sans photo' }}</div>
            </div>
            <div class="album-meta">
              <span class="album-name">
                {{ r.name }}
                <span v-if="r.incomplete" class="recipe-badge" title="Champs manquants : empreinte non appariable">incomplète</span>
              </span>
              <span v-if="r.author" class="recipe-author">{{ r.author }}</span>
              <span class="album-count">
                {{ r.fields.filmSimulation ?? '—' }} · {{ r.photoCount }} photo{{ r.photoCount > 1 ? 's' : '' }}
              </span>
            </div>
          </div>
        </div>
      </template>
    </div>

    <RecipeEditor v-model:show="showEditor" @saved="onSaved" />

    <!-- Rapport d'import : ce qui est passé, ce qui a été ignoré et pourquoi. -->
    <n-modal :show="report !== null" @update:show="(v: boolean) => { if (!v) report = null }">
      <div v-if="report" class="recipe-dialog" role="dialog" aria-labelledby="ri-title">
        <span id="ri-title" class="re-title">Rapport d’import</span>
        <p class="ri-summary">
          {{ report.imported }} importée{{ report.imported > 1 ? 's' : '' }} ·
          {{ report.incomplete }} incomplète{{ report.incomplete > 1 ? 's' : '' }} ·
          {{ report.skipped }} ignorée{{ report.skipped > 1 ? 's' : '' }} ·
          {{ report.errors }} erreur{{ report.errors > 1 ? 's' : '' }}
        </p>
        <ul class="ri-items">
          <li v-for="(it, i) in report.items" :key="i" :class="'ri-item ri-item--' + it.status">
            <strong>{{ it.name }}</strong> — {{ statusLabels[it.status] ?? it.status }}<template v-if="it.message"> : {{ it.message }}</template>
          </li>
        </ul>
        <div class="re-actions">
          <n-button type="primary" @click="report = null">Fermer</n-button>
        </div>
      </div>
    </n-modal>
  </div>
</template>
