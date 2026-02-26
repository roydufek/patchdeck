/**
 * Returns a human-readable relative time string from a date string or Date object.
 * e.g. "just now", "3 min ago", "2 hours ago", "yesterday", "3 days ago"
 */
export function timeAgo(dateString) {
  if (!dateString) return 'never'
  const d = new Date(dateString)
  if (Number.isNaN(d.getTime())) return 'unknown'

  const now = Date.now()
  const deltaMs = now - d.getTime()
  const absDelta = Math.abs(deltaMs)
  const future = deltaMs < 0

  if (absDelta < 60_000) return 'just now'

  const minutes = Math.floor(absDelta / 60_000)
  const hours = Math.floor(absDelta / 3_600_000)
  const days = Math.floor(absDelta / 86_400_000)
  const weeks = Math.floor(days / 7)
  const months = Math.floor(days / 30)

  let label
  if (minutes < 60) {
    label = `${minutes} min`
  } else if (hours < 24) {
    label = hours === 1 ? '1 hour' : `${hours} hours`
  } else if (days === 1) {
    return future ? 'tomorrow' : 'yesterday'
  } else if (days < 7) {
    label = `${days} days`
  } else if (weeks < 5) {
    label = weeks === 1 ? '1 week' : `${weeks} weeks`
  } else if (months < 12) {
    label = months === 1 ? '1 month' : `${months} months`
  } else {
    const years = Math.floor(days / 365)
    label = years === 1 ? '1 year' : `${years} years`
  }

  return future ? `in ${label}` : `${label} ago`
}

/**
 * Returns the full formatted date string for use in title attributes.
 */
export function fullDate(dateString) {
  if (!dateString) return ''
  const d = new Date(dateString)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString()
}
