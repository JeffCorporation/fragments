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
