import { api } from '../api'
import { Busy, Notice, ProgressBar, Spinner, StateBadge, progressText, stuckReason } from '../components'
import { bytes, plural, rate } from '../format'
import { useAction } from '../hooks'
import { href } from '../router'
import { useStatus } from '../status'

export default function Downloads() {
  const status = useStatus()
  const action = useAction()
  if (!status) return <Spinner label="Loading…" />
  const s = status.status

  const fail = (id: number) => {
    if (confirm('Blocklist this release and look for another one?')) action.run(() => api.failDownload(id), `fail${id}`)
  }

  return (
    <>
      <div className="page-head">
        <h1>Downloads</h1>
        <button className="button" disabled={action.busy} onClick={() => action.run(api.searchNow, 'search')}>
          <Busy on={action.pending === 'search'} label="Starting…">
            Search for wanted now
          </Busy>
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
      <Transfer />

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
                <Busy on={action.pending === `fail${p.id}`}>Fail</Busy>
              </button>
            </div>
            <ProgressBar percent={p.percent} />
            <div className="small muted">{progressText(p)}</div>
            {stuckReason(p) && <div className="small stuck">{stuckReason(p)}</div>}
          </div>
        ))}
      </section>

      {s.queued.length > 0 && (
        <section>
          <h2>Queued</h2>
          <p className="muted small">
            {s.limits.maxDownloads > 0
              ? `${plural(s.limits.maxDownloads, 'download')} run at a time; these start in this order as others finish.`
              : 'These start in this order.'}
          </p>
          <ul className="list">
            {s.queued.map((p) => (
              <li key={p.id}>
                <a href={href(`/media/${p.mediaId}`)} className="release-name">
                  {p.position}. {p.name}
                </a>
                <span className="small muted nowrap">{p.total > 0 && bytes(p.total)}</span>
                <button className="button small" onClick={() => fail(p.id)} disabled={action.busy}>
                  <Busy on={action.pending === `fail${p.id}`}>Fail</Busy>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}

      {s.seeding.length > 0 && (
        <section>
          <h2>Seeding</h2>
          <p className="muted small">
            {status.seedRatio > 0
              ? `Each stops seeding at ratio ${status.seedRatio}, or when unified stops.`
              : 'These seed until unified stops. Set a seed ratio in the settings to stop sooner.'}
            {s.limits.maxSeeds > 0 && ` ${plural(s.limits.maxSeeds, 'download')} seed at a time; the rest wait their turn.`}
          </p>
          <ul className="list">
            {s.seeding.map((p) => (
              <li key={p.id}>
                <a href={href(`/media/${p.mediaId}`)} className="release-name">
                  {p.name}
                </a>
                <span className="small muted nowrap">
                  {p.paused
                    ? `waiting to seed · ratio ${p.ratio.toFixed(2)}`
                    : `ratio ${p.ratio.toFixed(2)} · ${bytes(p.uploaded)} up · ${plural(p.peers, 'peer')}`}
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

/** The speed limits in force, and how peers are connected. */
function Transfer() {
  const status = useStatus()
  if (!status) return null
  const { limits, active, seeding } = status.status
  const running = [...active, ...seeding.filter((p) => !p.paused)]
  const utp = running.reduce((n, p) => n + p.utp, 0)
  const tcp = running.reduce((n, p) => n + p.tcp, 0)
  const limit = (n: number) => (n > 0 ? rate(n) : 'no limit')
  const speeds =
    limits.download > 0 || limits.upload > 0
      ? `${limits.alt ? 'Alternative speed limits' : 'Speed limits'}: download ${limit(limits.download)}, upload ${limit(limits.upload)}`
      : 'No speed limits'
  return (
    <p className="muted small transfer-line">
      {speeds} (<a href={href('/settings')}>change</a>) ·{' '}
      <a href="api/debug/peers" target="_blank" rel="noreferrer">
        Peer details
      </a>
      {utp + tcp > 0 && (
        <>
          {' · '}
          {plural(utp + tcp, 'peer connection')}: {utp} uTP, {tcp} TCP
        </>
      )}
    </p>
  )
}
