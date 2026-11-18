import type { DiskStatus } from './api'
import { Notice } from './components'
import { bytes } from './format'
import { href } from './router'
import { useStatus } from './status'

/** A warning while new downloads are held back for lack of space. */
export function DiskWarning() {
  const d = useStatus()?.status.disk
  if (!d?.low) return null
  return (
    <Notice kind="warn">
      Low on disk space: {bytes(d.available)} free for downloads, less than the {bytes(d.minFree)} to keep free.
      Nothing new is downloaded until there is more space. Change the limit in <a href={href('/settings')}>Settings</a>.
    </Notice>
  )
}

/**
 * For a download picked by hand: a question to confirm it if it would leave
 * less than the space to keep free, or undefined when it fits.
 */
export function spaceQuestion(d: DiskStatus | undefined, size: number): string | undefined {
  if (!d || d.error || d.minFree === 0 || size <= 0 || d.available - size >= d.minFree) return undefined
  const left = Math.max(d.available - size, 0)
  return `This release is ${bytes(size)}. It would leave ${bytes(left)} free for downloads, less than the ${bytes(d.minFree)} to keep free. Grab it anyway?`
}
