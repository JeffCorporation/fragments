// takenAt is the camera's local wall-clock, stored (and sent) as a UTC instant
// ("...Z") even though it carries no real timezone. We format it WITH
// timeZone:'UTC' so the literal captured time is shown, never shifted to the
// browser's timezone.
const dateFmt = new Intl.DateTimeFormat('fr-FR', {
  timeZone: 'UTC',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
})

const dateTimeFmt = new Intl.DateTimeFormat('fr-FR', {
  timeZone: 'UTC',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

function toDate(takenAt: string | null): Date | null {
  if (!takenAt) return null
  const d = new Date(takenAt)
  return Number.isNaN(d.getTime()) ? null : d
}

/** Date only, e.g. "4 oct. 2025" — for the gallery tiles. */
export function formatDate(takenAt: string | null): string {
  const d = toDate(takenAt)
  return d ? dateFmt.format(d) : ''
}

/** Date + time, e.g. "4 oct. 2025, 11:51" — for the detail/lightbox view. */
export function formatDateTime(takenAt: string | null): string {
  const d = toDate(takenAt)
  return d ? dateTimeFmt.format(d) : ''
}

/** Byte size in French units, e.g. "4,2 Mo" — for object sizes and freed space. */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} o`
  const units = ['Ko', 'Mo', 'Go', 'To']
  let v = bytes
  let i = -1
  do {
    v /= 1024
    i++
    // Advance on the DISPLAYED value: 1 048 550 octets arrondis à un chiffre
    // donnent 1024,0 Ko — qui doit s'afficher « 1 Mo », pas « 1 024 Ko ».
  } while (Math.round(v * 10) / 10 >= 1024 && i < units.length - 1)
  return `${v.toLocaleString('fr-FR', { maximumFractionDigits: 1 })} ${units[i]}`
}
