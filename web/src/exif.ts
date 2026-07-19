// Helpers for the on-demand EXIF/Fujifilm detail panel. The backend stores the
// full tag dump as a JSON string ("exif_json") of the shape
//   { "exif": { TagName: formattedValue, ... }, "fuji": { Name: value, ... } }
// where EXIF values are already human-formatted strings from the parser and the
// Fujifilm values are the recognized maker-note tags (mostly raw numeric codes,
// plus the decoded "FilmSimulation" name). These helpers turn that into sorted,
// display-ready rows; they never throw on malformed input.

export interface ExifEntry {
  /** Raw tag name, e.g. "ExposureBiasValue". */
  key: string
  /** Spaced, readable label, e.g. "Exposure Bias Value". */
  label: string
  /** Stringified value, trimmed; empty entries are dropped. */
  value: string
}

export interface ParsedExif {
  exif: ExifEntry[]
  fuji: ExifEntry[]
}

// Tags carrying binary blobs rather than human-readable detail. MakerNote is the
// raw Fujifilm block we already decode into the "fuji" section, so showing its
// undecoded bytes here would be noise.
const HIDDEN_TAGS = new Set(['MakerNote', 'PrintImageMatching', 'ComponentsConfiguration'])

// parseExifJson turns the stored exif_json string into sorted display rows.
// Returns null for an empty/invalid payload (a photo cataloged without EXIF).
export function parseExifJson(raw: string): ParsedExif | null {
  if (!raw) return null
  let obj: unknown
  try {
    obj = JSON.parse(raw)
  } catch {
    return null
  }
  // JSON.parse can yield a scalar or null ("null", "5", ...); only an object has
  // the {exif,fuji} shape, and guarding here keeps the documented no-throw contract.
  if (typeof obj !== 'object' || obj === null) return null
  const { exif, fuji } = obj as { exif?: Record<string, unknown>; fuji?: Record<string, unknown> }
  const exifRows = toEntries(exif)
  const fujiRows = toEntries(fuji)
  if (exifRows.length === 0 && fujiRows.length === 0) return null
  return { exif: exifRows, fuji: fujiRows }
}

function toEntries(m: Record<string, unknown> | undefined): ExifEntry[] {
  if (!m || typeof m !== 'object') return []
  return Object.keys(m)
    .filter((k) => !HIDDEN_TAGS.has(k))
    .map((k) => ({ key: k, label: humanizeKey(k), value: stringifyValue(m[k]) }))
    .filter((e) => e.value !== '')
    .sort((a, b) => a.label.localeCompare(b.label, 'fr'))
}

function stringifyValue(v: unknown): string {
  if (v == null) return ''
  if (typeof v === 'string') return v.trim()
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return JSON.stringify(v)
}

// humanizeKey inserts spaces into a CamelCase / acronym tag name:
//   "ExposureTime"          -> "Exposure Time"
//   "GPSLatitudeRef"        -> "GPS Latitude Ref"
//   "FocalLengthIn35mmFilm" -> "Focal Length In35mm Film"
export function humanizeKey(key: string): string {
  return key
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2') // ACRONYM↔Word boundary
    .replace(/([a-z\d])([A-Z])/g, '$1 $2') // word↔Word boundary
    .trim()
}
