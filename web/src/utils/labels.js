/**
 * Human-readable display labels for internal enum values.
 * API values remain unchanged — these are UI-only transforms.
 */

/** Auto-update policy label */
export function policyLabel(value) {
  switch (value) {
    case 'scheduled_apply': return 'Scheduled'
    case 'manual': return 'Manual'
    default: return value || 'Manual'
  }
}

/** Host key trust mode label */
export function trustModeLabel(value) {
  switch (value) {
    case 'tofu': return 'TOFU'
    case 'pinned': return 'Pinned'
    default: return value || 'TOFU'
  }
}
