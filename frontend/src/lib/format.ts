const rupiah = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 })
const dateTime = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

const TIME_UNITS: [seconds: number, label: string][] = [
  [31536000, 'y'],
  [2592000, 'mo'],
  [86400, 'd'],
  [3600, 'h'],
  [60, 'm'],
]

export function formatRupiah(amount: number): string {
  return `Rp ${rupiah.format(amount)}`
}

export function formatDateTime(iso: string): string {
  return dateTime.format(new Date(iso))
}

export function timeAgo(iso: string | null, now: number = Date.now()): string {
  if (iso === null) return 'Never'
  const seconds = Math.floor((now - new Date(iso).getTime()) / 1000)
  for (const [size, label] of TIME_UNITS) {
    if (seconds >= size) return `${Math.floor(seconds / size)}${label} ago`
  }
  return 'just now'
}
