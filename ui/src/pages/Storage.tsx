import { useState } from 'react'
import { api, type FolderUsage } from '../api'
import { Notice, Spinner } from '../components'
import { DiskWarning } from '../disk'
import { bytes, plural } from '../format'
import { useAction, useData } from '../hooks'
import { href } from '../router'

export default function StoragePage() {
  const report = useData(() => api.storage(), [], 60000)
  const measure = useAction()
  // Measures now instead of reusing the last report, then shows it.
  const remeasure = () => measure.run(() => api.storage(true)).then(report.reload)
  const r = report.data

  return (
    <>
      <div className="page-head">
        <h1>Storage</h1>
        <button className="button" disabled={measure.busy || !r} onClick={remeasure}>
          {measure.busy ? 'Measuring…' : 'Measure again'}
        </button>
      </div>
      <DiskWarning />
      {(report.error ?? measure.error) && <Notice kind="error">{report.error ?? measure.error}</Notice>}
      {report.loading && !r && <Spinner label="Measuring folders…" />}
      {r && (
        <>
          <p className="muted small">
            {r.disk.minFree > 0 ? (
              <>
                Nothing new is downloaded when it would leave less than {bytes(r.disk.minFree)} free for downloads.{' '}
              </>
            ) : (
              <>Free space is not checked before downloading. </>
            )}
            <a href={href('/settings')}>Change this in Settings</a>. Measured at{' '}
            {new Date(r.measuredAt).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}.
          </p>
          <div className="folders">
            {r.folders.map((f) => (
              <Folder key={f.label} folder={f} />
            ))}
          </div>
        </>
      )}
    </>
  )
}

const SHOWN = 10

function Folder({ folder: f }: { folder: FolderUsage }) {
  const [all, setAll] = useState(false)
  const used = f.total - f.free
  const share = (n: number) => (f.total > 0 ? `${Math.min((n / f.total) * 100, 100)}%` : '0%')
  const largest = f.entries[0]?.size ?? 0
  const entries = all ? f.entries : f.entries.slice(0, SHOWN)

  return (
    <section className="panel folder">
      <div className="folder-head">
        <h2>{f.label}</h2>
        <code className="small">{f.path}</code>
      </div>
      {f.error && <span className="error-text">{f.error}</span>}

      <div className="folder-size">
        <strong>{bytes(f.size)}</strong>
        <span className="muted small">{plural(f.files, 'file')}</span>
      </div>
      {f.linked > 0 && (
        <p className="muted small">
          {bytes(f.linked)} of this is hardlinked{' '}
          {f.label === 'Downloads' ? 'into the library while it seeds' : 'from Downloads while it seeds'}, so it is
          stored only once.
        </p>
      )}

      {f.total > 0 && (
        <div className="folder-disk">
          <div className="disk-bar stacked" aria-hidden="true">
            <div className="disk-this" style={{ width: share(f.size) }} />
            <div className="disk-other" style={{ width: share(Math.max(used - f.size, 0)) }} />
          </div>
          <span className="small">
            <strong>{bytes(f.free)} free</strong> of {bytes(f.total)}
            {f.sameDiskAs.length > 0 && <span className="muted"> · same disk as {f.sameDiskAs.join(' and ')}</span>}
          </span>
        </div>
      )}

      {f.entries.length === 0 ? (
        <p className="muted">Empty.</p>
      ) : (
        <ol className="entries">
          {entries.map((e) => (
            <li key={e.name}>
              <span className="entry-name" title={e.name}>
                {e.name}
              </span>
              <span className="entry-size small">{bytes(e.size)}</span>
              <span className="entry-bar" aria-hidden="true">
                <span style={{ width: `${largest > 0 ? (e.size / largest) * 100 : 0}%` }} />
              </span>
            </li>
          ))}
        </ol>
      )}
      {f.entries.length > SHOWN && (
        <button className="button small" onClick={() => setAll(!all)}>
          {all ? 'Show fewer' : `Show all ${f.entries.length}`}
        </button>
      )}
    </section>
  )
}
