<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NButton, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, useMessage } from 'naive-ui'
import type { Recipe, RecipeFields, RecipeSchema } from '../api/client'
import { useRecipesStore } from '../stores/recipes'

// Éditeur de recette partagé : « + Nouvelle recette » (vierge), « Modifier »
// (pré-rempli depuis la fiche) et « Nommer cette recette » (pré-rempli depuis
// les champs décodés d'une photo). Tous les champs de rendu sont contraints au
// vocabulaire canonique servi par /api/recipes/schema : une recette saisie
// doit hacher exactement comme une photo décodée, sinon l'appariement casse en
// silence. Seuls nom, notes et crédit sont du texte libre.

const props = defineProps<{
  show: boolean
  recipe?: Recipe | null // mode édition
  prefill?: RecipeFields | null // mode « nommer depuis une photo »
}>()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'saved', recipe: Recipe): void
}>()

const recipes = useRecipesStore()
const message = useMessage()

const schema = ref<RecipeSchema | null>(null)
const saving = ref(false)

// Les champs numériques sont nullables : n-input-number émet null quand on
// vide le champ. save() les ramène aux défauts du schéma pour que la recette
// reste « complète par construction » (features.md).
const form = reactive<{
  name: string
  notes: string
  author: string
  authorUrl: string
  source: string
  filmSimulation: string
  dynamicRange: string
  dRangePriority: string
  highlightTone: number | null
  shadowTone: number | null
  color: number | null
  sharpness: number | null
  noiseReduction: number | null
  clarity: number | null
  grainEffect: string
  grainSize: string
  colorChrome: string
  colorChromeFXBlue: string
  whiteBalance: string
  colorTemperature: number | null
  wbShiftRed: number | null
  wbShiftBlue: number | null
  monochromaticWC: number | null
  monochromaticMG: number | null
}>({
  name: '',
  notes: '',
  author: '',
  authorUrl: '',
  source: '',
  filmSimulation: 'Provia/Standard',
  dynamicRange: 'Auto',
  dRangePriority: 'Off',
  highlightTone: 0,
  shadowTone: 0,
  color: 0,
  sharpness: 0,
  noiseReduction: 0,
  clarity: 0,
  grainEffect: 'Off',
  grainSize: 'Off',
  colorChrome: 'Off',
  colorChromeFXBlue: 'Off',
  whiteBalance: 'Auto',
  colorTemperature: 5600,
  wbShiftRed: 0,
  wbShiftBlue: 0,
  monochromaticWC: 0,
  monochromaticMG: 0,
})

// Réinitialise le formulaire à chaque ouverture : défauts « boîtier sorti de
// boîte », recouverts par la recette éditée ou les champs décodés de la photo.
// Une recette importée incomplète est ainsi complétée par construction.
watch(
  () => props.show,
  async (show) => {
    if (!show) return
    try {
      schema.value = await recipes.ensureSchema()
    } catch (e) {
      message.error(e instanceof Error ? e.message : 'Échec du chargement du vocabulaire')
      emit('update:show', false)
      return
    }
    const overlay: RecipeFields = { ...schema.value.defaults, ...defined(props.recipe?.fields ?? props.prefill ?? {}) }
    form.name = props.recipe?.name ?? ''
    form.notes = props.recipe?.notes ?? ''
    form.author = props.recipe?.author ?? ''
    form.authorUrl = props.recipe?.authorUrl ?? ''
    form.source = props.recipe?.source ?? ''
    form.filmSimulation = overlay.filmSimulation ?? 'Provia/Standard'
    form.dynamicRange = overlay.dynamicRange ?? 'Auto'
    form.dRangePriority = overlay.dRangePriority ?? 'Off'
    form.highlightTone = overlay.highlightTone ?? 0
    form.shadowTone = overlay.shadowTone ?? 0
    form.color = overlay.color ?? 0
    form.sharpness = overlay.sharpness ?? 0
    form.noiseReduction = overlay.noiseReduction ?? 0
    form.clarity = overlay.clarity ?? 0
    form.grainEffect = overlay.grainEffect ?? 'Off'
    form.grainSize = overlay.grainSize ?? 'Off'
    form.colorChrome = overlay.colorChrome ?? 'Off'
    form.colorChromeFXBlue = overlay.colorChromeFXBlue ?? 'Off'
    form.whiteBalance = overlay.whiteBalance ?? 'Auto'
    form.colorTemperature = overlay.colorTemperature ?? 5600
    form.wbShiftRed = overlay.wbShiftRed ?? 0
    form.wbShiftBlue = overlay.wbShiftBlue ?? 0
    form.monochromaticWC = overlay.monochromaticWC ?? 0
    form.monochromaticMG = overlay.monochromaticMG ?? 0
  },
)

// defined retire les clés absentes/undefined pour que les défauts survivent au
// spread (une recette partielle ne doit pas écraser un défaut par undefined).
function defined(f: RecipeFields): RecipeFields {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== null) out[k] = v
  return out as RecipeFields
}

const isMono = computed(() => schema.value?.monochromeSimulations.includes(form.filmSimulation) ?? false)
const isKelvin = computed(() => form.whiteBalance === 'Kelvin')
const grainOff = computed(() => form.grainEffect === 'Off')
const bounds = computed(() => schema.value?.bounds ?? {})

const opts = (list: string[] | undefined) => (list ?? []).map((v) => ({ label: v, value: v }))

// Le menu grain du boîtier est un réglage combiné : un effet actif impose une
// taille Small/Large. On garde les deux valeurs cohérentes en direct plutôt
// que d'attendre un 400 (empreinte qu'aucun boîtier ne produirait).
watch(
  () => form.grainEffect,
  (v) => {
    if (v === 'Off') form.grainSize = 'Off'
    else if (form.grainSize === 'Off') form.grainSize = 'Small'
  },
)
const grainSizeOptions = computed(() =>
  opts(schema.value?.grainSizes).filter((o) => grainOff.value || o.value !== 'Off'),
)

// Avertissement de ré-appariement : modifier un paramètre de rendu d'une
// recette qui a des photos appariées les rend anonymes (comportement voulu,
// mais à annoncer).
const repairWarning = computed(() => !!props.recipe && props.recipe.photoCount > 0)

async function save() {
  const name = form.name.trim()
  if (!name) {
    message.error('Le nom de la recette est requis')
    return
  }
  // « Complète par construction » : un champ numérique vidé retombe sur le
  // défaut du schéma, jamais sur un null qui rendrait la recette incomplète.
  const d = schema.value?.defaults ?? {}
  const fields: RecipeFields = {
    filmSimulation: form.filmSimulation,
    dynamicRange: form.dynamicRange,
    dRangePriority: form.dRangePriority,
    highlightTone: form.highlightTone ?? d.highlightTone ?? 0,
    shadowTone: form.shadowTone ?? d.shadowTone ?? 0,
    color: form.color ?? d.color ?? 0,
    sharpness: form.sharpness ?? d.sharpness ?? 0,
    noiseReduction: form.noiseReduction ?? d.noiseReduction ?? 0,
    clarity: form.clarity ?? d.clarity ?? 0,
    grainEffect: form.grainEffect,
    grainSize: form.grainSize,
    colorChrome: form.colorChrome,
    colorChromeFXBlue: form.colorChromeFXBlue,
    whiteBalance: form.whiteBalance,
    wbShiftRed: form.wbShiftRed ?? d.wbShiftRed ?? 0,
    wbShiftBlue: form.wbShiftBlue ?? d.wbShiftBlue ?? 0,
    monochromaticWC: form.monochromaticWC ?? 0,
    monochromaticMG: form.monochromaticMG ?? 0,
  }
  if (isKelvin.value) fields.colorTemperature = form.colorTemperature ?? 5600
  const body = {
    name,
    fields,
    notes: form.notes.trim(),
    author: form.author.trim(),
    authorUrl: form.authorUrl.trim(),
    source: form.source.trim(),
  }
  saving.value = true
  try {
    const saved = props.recipe
      ? await recipes.update(props.recipe.id, body)
      : await recipes.create(body)
    emit('update:show', false)
    emit('saved', saved)
  } catch (e) {
    message.error(e instanceof Error ? e.message : 'Échec de l’enregistrement')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => emit('update:show', v)">
    <div class="recipe-dialog" role="dialog" aria-labelledby="re-title">
      <span id="re-title" class="re-title">
        {{ recipe ? `Modifier « ${recipe.name} »` : 'Nouvelle recette' }}
      </span>

      <div v-if="!schema" class="status">chargement…</div>
      <template v-else>
        <n-form class="re-grid" :show-feedback="false" @submit.prevent="save">
          <n-form-item class="re-row re-row--wide" label="Nom">
            <n-input v-model:value="form.name" size="small" placeholder="Kodachrome 64" />
          </n-form-item>

          <n-form-item class="re-row re-row--wide" label="Simulation de film">
            <n-select v-model:value="form.filmSimulation" size="small" :options="opts(schema.filmSimulations)" />
          </n-form-item>
          <n-form-item class="re-row" label="Plage dynamique">
            <n-select v-model:value="form.dynamicRange" size="small" :options="opts(schema.dynamicRanges)" />
          </n-form-item>
          <n-form-item class="re-row" label="Priorité D-Range">
            <n-select v-model:value="form.dRangePriority" size="small" :options="opts(schema.dRangePriorities)" />
          </n-form-item>

          <n-form-item class="re-row" label="Hautes lumières">
            <n-input-number v-model:value="form.highlightTone" size="small"
              :min="bounds.tone?.min" :max="bounds.tone?.max" :step="bounds.tone?.step" />
          </n-form-item>
          <n-form-item class="re-row" label="Ombres">
            <n-input-number v-model:value="form.shadowTone" size="small"
              :min="bounds.tone?.min" :max="bounds.tone?.max" :step="bounds.tone?.step" />
          </n-form-item>
          <n-form-item class="re-row" label="Couleur" :title="isMono ? 'Sans objet sur une simulation monochrome' : undefined">
            <n-input-number v-model:value="form.color" size="small" :disabled="isMono"
              :min="bounds.color?.min" :max="bounds.color?.max" />
          </n-form-item>
          <n-form-item class="re-row" label="Netteté">
            <n-input-number v-model:value="form.sharpness" size="small"
              :min="bounds.sharpness?.min" :max="bounds.sharpness?.max" />
          </n-form-item>
          <n-form-item class="re-row" label="Réduction du bruit">
            <n-input-number v-model:value="form.noiseReduction" size="small"
              :min="bounds.noiseReduction?.min" :max="bounds.noiseReduction?.max" />
          </n-form-item>
          <n-form-item class="re-row" label="Clarté">
            <n-input-number v-model:value="form.clarity" size="small"
              :min="bounds.clarity?.min" :max="bounds.clarity?.max" />
          </n-form-item>

          <n-form-item class="re-row" label="Grain">
            <n-select v-model:value="form.grainEffect" size="small" :options="opts(schema.strengths)" />
          </n-form-item>
          <n-form-item class="re-row" label="Taille du grain" :title="grainOff ? 'Sans objet quand le grain est désactivé' : undefined">
            <n-select v-model:value="form.grainSize" size="small" :disabled="grainOff" :options="grainSizeOptions" />
          </n-form-item>
          <n-form-item class="re-row" label="Color Chrome">
            <n-select v-model:value="form.colorChrome" size="small" :options="opts(schema.strengths)" />
          </n-form-item>
          <n-form-item class="re-row" label="Color Chrome FX Blue">
            <n-select v-model:value="form.colorChromeFXBlue" size="small" :options="opts(schema.strengths)" />
          </n-form-item>

          <n-form-item class="re-row re-row--wide" label="Balance des blancs">
            <n-select v-model:value="form.whiteBalance" size="small" :options="opts(schema.whiteBalances)" />
          </n-form-item>
          <n-form-item class="re-row" label="Température (K)" :title="!isKelvin ? 'Seulement en balance Kelvin' : undefined">
            <n-input-number v-model:value="form.colorTemperature" size="small" :disabled="!isKelvin"
              :min="bounds.colorTemperature?.min" :max="bounds.colorTemperature?.max" :step="bounds.colorTemperature?.step" />
          </n-form-item>
          <span class="re-row re-row--pair">
            <n-form-item class="re-sub" label="Décalage R">
              <n-input-number v-model:value="form.wbShiftRed" size="small"
                :min="bounds.wbShift?.min" :max="bounds.wbShift?.max" />
            </n-form-item>
            <n-form-item class="re-sub" label="Décalage B">
              <n-input-number v-model:value="form.wbShiftBlue" size="small"
                :min="bounds.wbShift?.min" :max="bounds.wbShift?.max" />
            </n-form-item>
          </span>

          <span class="re-row re-row--pair" :title="!isMono ? 'Seulement sur les simulations N&B / Acros' : undefined">
            <n-form-item class="re-sub" label="Mono WC (chaud/froid)">
              <n-input-number v-model:value="form.monochromaticWC" size="small" :disabled="!isMono"
                :min="bounds.monochromatic?.min" :max="bounds.monochromatic?.max" />
            </n-form-item>
            <n-form-item class="re-sub" label="Mono MG (magenta/vert)">
              <n-input-number v-model:value="form.monochromaticMG" size="small" :disabled="!isMono"
                :min="bounds.monochromatic?.min" :max="bounds.monochromatic?.max" />
            </n-form-item>
          </span>

          <n-form-item class="re-row re-row--wide" label="Notes (ISO, expo, conditions…)">
            <n-input v-model:value="form.notes" type="textarea" size="small" :autosize="{ minRows: 2, maxRows: 4 }"
              placeholder="ISO auto max 6400, expo +1/3 typique" />
          </n-form-item>

          <n-form-item class="re-row" label="Auteur">
            <n-input v-model:value="form.author" size="small" placeholder="Ritchie Roesch" />
          </n-form-item>
          <n-form-item class="re-row" label="Source">
            <n-input v-model:value="form.source" size="small" placeholder="Fuji X Weekly" />
          </n-form-item>
          <n-form-item class="re-row re-row--wide" label="Lien (http/https)">
            <n-input v-model:value="form.authorUrl" size="small" placeholder="https://fujixweekly.com/…" />
          </n-form-item>
        </n-form>

        <p v-if="repairWarning" class="re-warning">
          {{ recipe!.photoCount }} photo{{ recipe!.photoCount > 1 ? 's sont appariées' : ' est appariée' }}
          à cette recette : modifier un réglage recalcule l’empreinte et ré-apparie les photos en conséquence.
        </p>

        <div class="re-actions">
          <n-button tertiary @click="emit('update:show', false)">Annuler</n-button>
          <n-button type="primary" :loading="saving" @click="save">Enregistrer</n-button>
        </div>
      </template>
    </div>
  </n-modal>
</template>
