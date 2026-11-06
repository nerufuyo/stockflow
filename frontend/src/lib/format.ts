const rupiah = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 })

/** 1234567 -> "Rp 1.234.567" */
export function formatRupiah(amount: number): string {
  return `Rp ${rupiah.format(amount)}`
}

const dateTime = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

export function formatDateTime(iso: string): string {
  return dateTime.format(new Date(iso))
}

/** "2h ago" style label; null means the product has never moved. */
export function timeAgo(iso: string | null, now: number = Date.now()): string {
  if (iso === null) return 'Never'
  const seconds = Math.floor((now - new Date(iso).getTime()) / 1000)
  if (seconds < 60) return 'just now' // includes small clock skew, where the difference is negative
  const units: [number, string][] = [
    [31536000, 'y'],
    [2592000, 'mo'],
    [86400, 'd'],
    [3600, 'h'],
    [60, 'm'],
  ]
  for (const [size, label] of units) {
    if (seconds >= size) return `${Math.floor(seconds / size)}${label} ago`
  }
  return 'just now'
}
