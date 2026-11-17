import { api } from '../api'
import { Notice, ProgressBar, Spinner, StateBadge, progressText } from '../components'
import { bytes, plural } from '../format'
import { useAction } from '../hooks'
import { href } from '../router'
import { useStatus } from '../status'

export default function Downloads() {
  const status = useStatus()
  const action = useAction()
  if (!status) return <Spinner label="Loading…" />
  const s = status.status

  const fail = (id: number) => {
    if (confirm('Blocklist this release and look for another one?')) action.run(() => api.failDownload(id))
  }

  return (
    <>
      <div className="page-head">
        <h1>Downloads</h1>
        <button className="button" disabled={action.busy} onClick={() => action.run(api.searchNow)}>
          Search for wanted now
        </button>
      </div>
      {s.vpnError ? (
        <Notice kind="error">{s.vpnError}. Torrents are paused until the VPN is back.</Notice>
      ) : s.vpn ? (
        <p className="vpn-line">
          <span className="dot dot-up" /> Torrent traffic is bound to {s.vpn}
        </p>
      ) : (
        <Notice kind="warn">
          Torrent traffic is not bound to a VPN. Set <code>vpn_interface</code> in the config file.
        </Notice>
      )}
      {action.error && <Notice kind="error">{action.error}</Notice>}

      <section>
        <h2>Active</h2>
        {s.active.length === 0 && <p className="muted">Nothing downloading.</p>}
        {s.active.map((p) => (
          <div className="download" key={p.id}>
            <div className="download-head">
              <a href={href(`/media/${p.mediaId}`)} className="release-name">
                {p.name}
              </a>
              <button className="button small" onClick={() => fail(p.id)} disabled={action.busy}>
                Fail
              </button>
            </div>
            <ProgressBar percent={p.percent} />
            <div className="small muted">{progressText(p)}</div>
          </div>
        ))}
      </section>

      {s.seeding.length > 0 && (
        <section>
          <h2>Seeding</h2>
          <p className="muted small">
            {status.seedRatio > 0
              ? `Each stops seeding at ratio ${status.seedRatio}, or when unified stops.`
              : 'These seed until unified stops. Set a seed ratio in the settings to stop sooner.'}
          </p>
          <ul className="list">
            {s.seeding.map((p) => (
              <li key={p.id}>
                <a href={href(`/media/${p.mediaId}`)} className="release-name">
                  {p.name}
                </a>
                <span className="small muted nowrap">
                  ratio {p.ratio.toFixed(2)} · {bytes(p.uploaded)} up · {plural(p.peers, 'peer')}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {status.recent.length > 0 && (
        <section>
          <h2>Recent</h2>
          <ul className="list">
            {status.recent.map((d) => (
              <li key={d.id}>
                <span className="release-name">{d.name}</span>
                <StateBadge state={d.state} />
              </li>
            ))}
          </ul>
        </section>
      )}

      {s.log.length > 0 && (
        <section>
          <h2>Activity</h2>
          <pre className="log">{s.log.join('\n')}</pre>
        </section>
      )}
    </>
  )
}
