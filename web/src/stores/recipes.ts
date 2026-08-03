import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { Recipe, RecipeBody, RecipeImportReport, RecipeSchema } from '../api/client'
import { clearPhotoDetailCache } from '../composables/usePhotoDetail'

export const useRecipesStore = defineStore('recipes', {
  state: () => ({
    list: [] as Recipe[],
    schema: null as RecipeSchema | null,
    loaded: false,
    loading: false,
  }),
  actions: {
    async fetch() {
      this.loading = true
      try {
        const r = await api.get<{ recipes: Recipe[] }>('/api/recipes')
        this.list = r.recipes ?? []
        this.loaded = true
      } finally {
        this.loading = false
      }
    },
    async ensure() {
      if (!this.loaded && !this.loading) await this.fetch()
    },
    // Le vocabulaire canonique ne change qu'avec le binaire : un seul fetch.
    async ensureSchema(): Promise<RecipeSchema> {
      if (!this.schema) this.schema = await api.get<RecipeSchema>('/api/recipes/schema')
      return this.schema
    },
    async create(body: RecipeBody): Promise<Recipe> {
      const recipe = await api.post<Recipe>('/api/recipes', body)
      // La liste est triée par nom côté serveur ; un refetch la remet en ordre
      // (compteurs de photos compris, que le client ne sait pas calculer). Le
      // cache photo-détail est purgé : recipeName/recipeId y sont dérivés de
      // l'empreinte et viennent de changer pour toutes les photos appariées.
      clearPhotoDetailCache()
      await this.fetch()
      return recipe
    },
    async update(id: number, body: RecipeBody): Promise<Recipe> {
      const recipe = await api.patch<Recipe>(`/api/recipes/${id}`, body)
      clearPhotoDetailCache()
      await this.fetch()
      return recipe
    },
    async remove(id: number) {
      await api.del(`/api/recipes/${id}`)
      clearPhotoDetailCache()
      this.list = this.list.filter((r) => r.id !== id)
    },
    async importFile(entries: unknown): Promise<RecipeImportReport> {
      const report = await api.post<RecipeImportReport>('/api/recipes/import', entries)
      clearPhotoDetailCache()
      await this.fetch()
      return report
    },
  },
})
