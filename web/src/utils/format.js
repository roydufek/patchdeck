export function formatTimestamp(ts) {
  if (!ts) return 'unknown'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return 'unknown'
  return d.toLocaleString()
}

export function formatRelativeTime(ts) {
  if (!ts) return 'unknown'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return 'unknown'
  const deltaMs = Date.now() - d.getTime()
  const absMs = Math.abs(deltaMs)
  const suffix = deltaMs >= 0 ? 'ago' : 'from now'

  if (absMs < 60 * 1000) return 'just now'
  if (absMs < 60 * 60 * 1000) return `${Math.round(absMs / (60 * 1000))}m ${suffix}`
  if (absMs < 24 * 60 * 60 * 1000) return `${Math.round(absMs / (60 * 60 * 1000))}h ${suffix}`
  return `${Math.round(absMs / (24 * 60 * 60 * 1000))}d ${suffix}`
}

export function staleLabel(ts) {
  if (!ts) return null
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return null
  const ageMs = Date.now() - d.getTime()
  if (ageMs < 60 * 60 * 1000) return 'fresh'
  if (ageMs < 24 * 60 * 60 * 1000) return 'today'
  return 'stale'
}

export function newestTimestamp(...values) {
  const timestamps = values
    .map(v => {
      if (!v) return null
      const ms = Date.parse(v)
      return Number.isFinite(ms) ? ms : null
    })
    .filter(v => v !== null)

  if (timestamps.length === 0) return ''
  return new Date(Math.max(...timestamps)).toISOString()
}

export function formatFingerprintShort(value) {
  const normalized = (value || '').trim()
  if (!normalized) return 'none'
  if (normalized.length <= 22) return normalized
  return `${normalized.slice(0, 10)}…${normalized.slice(-10)}`
}

export function truncateText(value, max = 120) {
  const normalized = (value || '').trim()
  if (!normalized) return ''
  if (normalized.length <= max) return normalized
  return `${normalized.slice(0, Math.max(0, max - 1))}…`
}

export function isCronMacro(expr) {
  const normalized = (expr || '').trim().toLowerCase()
  return ['@yearly', '@annually', '@monthly', '@weekly', '@daily', '@midnight', '@hourly'].includes(normalized)
}

export function validateCronExpression(expr) {
  const normalized = (expr || '').trim()
  if (isCronMacro(normalized)) return ''

  const parts = normalized.split(/\s+/).filter(Boolean)
  if (parts.length !== 5) {
    return 'Cron expression must have 5 fields (minute hour day month weekday) or a macro like @daily'
  }

  const limits = [
    [0, 59],
    [0, 23],
    [1, 31],
    [1, 12],
    [0, 7]
  ]

  const labels = ['minute', 'hour', 'day', 'month', 'weekday']

  for (let i = 0; i < parts.length; i += 1) {
    const err = validateCronField(parts[i], limits[i][0], limits[i][1], labels[i])
    if (err) return `Invalid ${labels[i]} field: ${err}`
  }

  return ''
}

function cronValueToInt(value, label) {
  const v = (value || '').trim().toLowerCase()
  if (/^-?\d+$/.test(v)) return Number(v)

  if (label === 'month') {
    const monthNames = {
      jan: 1, january: 1, feb: 2, february: 2, mar: 3, march: 3,
      apr: 4, april: 4, may: 5, jun: 6, june: 6, jul: 7, july: 7,
      aug: 8, august: 8, sep: 9, september: 9, oct: 10, october: 10,
      nov: 11, november: 11, dec: 12, december: 12
    }
    if (Object.prototype.hasOwnProperty.call(monthNames, v)) return monthNames[v]
  }

  if (label === 'weekday') {
    const weekdayNames = {
      sun: 0, sunday: 0, mon: 1, monday: 1, tue: 2, tuesday: 2,
      wed: 3, wednesday: 3, thu: 4, thursday: 4, fri: 5, friday: 5,
      sat: 6, saturday: 6
    }
    if (Object.prototype.hasOwnProperty.call(weekdayNames, v)) return weekdayNames[v]
  }

  return NaN
}

function validateCronField(field, min, max, label) {
  const segments = field.split(',').map(s => s.trim())
  for (const seg of segments) {
    if (!seg) return 'empty segment'
    if (seg === '*') continue

    const [base, step] = seg.split('/')
    if (step !== undefined) {
      const stepVal = Number(step)
      if (!Number.isInteger(stepVal) || stepVal < 1) return 'step must be a positive integer'
    }

    if (base === '*') continue

    if (base.includes('-')) {
      const [loText, hiText] = base.split('-')
      const lo = cronValueToInt(loText, label)
      const hi = cronValueToInt(hiText, label)
      if (!Number.isInteger(lo) || !Number.isInteger(hi)) return 'range bounds must be integers or valid names'
      if (lo > hi) return 'range start cannot be greater than range end'
      if (lo < min || hi > max) return `range must be between ${min} and ${max}`
      continue
    }

    const val = cronValueToInt(base, label)
    if (!Number.isInteger(val)) return 'value must be an integer, *, range, step, or valid name'
    if (val < min || val > max) return `value must be between ${min} and ${max}`
  }

  return ''
}
