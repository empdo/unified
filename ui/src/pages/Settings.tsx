import { useEffect, useState, type ReactNode } from 'react'
import { api, type Indexer, type Quality, type Settings } from '../api'
import { Notice, Spinner } from '../components'
import { useAction, useData } from '../hooks'

const RESOLUTIONS = ['2160p', '1080p', '720p', '576p', '480p']

interface Form {
  tmdbKey: string
  indexers: Indexer[]
  quality: Quality
  seedRatio: number
  minFreeGB: number
  notifyURL: string
  newPassword: string
  removePassword: boolean
}

const toForm = (s: Settings): Form => ({
  tmdbKey: s.tmdbKey,
  indexers: s.indexers,
  quality: s.quality,
  seedRatio: s.seedRatio,
  minFreeGB: s.minFreeGB,
  notifyURL: s.notifyURL,
  newPassword: '',
  removePassword: false,
})

export default function SettingsPage() {
  const settings = useData(api.settings, [])
  const [form, setForm] = useState<Form>()
  const [saved, setSaved] = useState(false)
  const save = useAction()

  useEffect(() => {
    if (settings.data) setForm(toForm(settings.data))
  }, [settings.data])

  if (settings.error) return <Notice kind="error">{settings.error}</Notice>
  if (!form || !settings.data) return <Spinner label="Loading settings…" />
  const s = settings.data
  const dirty = JSON.stringify(form) !== JSON.stringify(toForm(s))

  const update = (change: Partial<Form>) => {
    setForm({ ...form, ...change })
    setSaved(false)
  }
  const updateQuality = (change: Partial<Quality>) => update({ quality: { ...form.quality, ...change } })

  const submit = async () => {
    const ok = await save.run(() =>
      api.saveSettings({
        tmdbKey: form.tmdbKey.trim(),
        indexers: form.indexers.map((ix) => ({ name: ix.name.trim(), url: ix.url.trim(), apiKey: ix.apiKey.trim() })),
        quality: form.quality,
        seedRatio: form.seedRatio,
        minFreeGB: form.minFreeGB,
        notifyURL: form.notifyURL.trim(),
        password: form.removePassword ? '' : form.newPassword || undefined,
      }),
    )
    if (ok) {
      setSaved(true)
      settings.reload()
    }
  }

  return (
    <div className="settings">
      <div className="page-head">
        <h1>Settings</h1>
      </div>

      <Section title="Indexers" hint="Torznab indexers, for example one per site in Prowlarr: http://host:9696/<id>/api">
        {form.indexers.length === 0 && <p className="muted">No indexers yet.</p>}
        {form.indexers.map((ix, i) => (
          <IndexerRow
            key={i}
            indexer={ix}
            onChange={(next) => update({ indexers: form.indexers.map((x, j) => (j === i ? next : x)) })}
            onRemove={() => update({ indexers: form.indexers.filter((_, j) => j !== i) })}
          />
        ))}
        <button
          className="button"
          onClick={() => update({ indexers: [...form.indexers, { name: '', url: '', apiKey: '' }] })}
        >
          Add indexer
        </button>
      </Section>

      <Section title="Metadata" hint="Titles, episode lists and posters come from TMDB.">
        <Field label="TMDB API key" help="Free from themoviedb.org → Settings → API." group>
          <SecretInput label="TMDB API key" value={form.tmdbKey} onChange={(v) => update({ tmdbKey: v })} />
        </Field>
      </Section>

      <Section title="Quality" hint="Which releases are accepted, and which are preferred.">
        <Field label="Resolutions" help="Accepted resolutions, most preferred first." group>
          <ResolutionList value={form.quality.resolutions} onChange={(resolutions) => updateQuality({ resolutions })} />
        </Field>
        <Field label="Preferred codecs" help="Comma-separated. Releases with one of these rank higher.">
          <input
            value={form.quality.preferCodecs.join(', ')}
            onChange={(e) =>
              updateQuality({
                preferCodecs: e.target.value
                  .split(',')
                  .map((c) => c.trim())
                  .filter(Boolean),
              })
            }
          />
        </Field>
        <div className="field-row">
          <Field label="Max movie size (GB)">
            <NumberInput value={form.quality.maxMovieGB} onChange={(maxMovieGB) => updateQuality({ maxMovieGB })} />
          </Field>
          <Field label="Max episode size (GB)">
            <NumberInput
              value={form.quality.maxEpisodeGB}
              onChange={(maxEpisodeGB) => updateQuality({ maxEpisodeGB })}
            />
          </Field>
          <Field label="Min seeders">
            <NumberInput
              value={form.quality.minSeeders}
              step={1}
              onChange={(minSeeders) => updateQuality({ minSeeders: Math.round(minSeeders) })}
            />
          </Field>
        </div>
      </Section>

      <Section title="Seeding" hint="Finished downloads keep seeding after they are imported.">
        <Field
          label="Seed ratio"
          help="Stop seeding once a download has uploaded this many times its size. 0 seeds until unified stops."
        >
          <NumberInput value={form.seedRatio} onChange={(seedRatio) => update({ seedRatio })} />
        </Field>
      </Section>

      <Section title="Storage" hint="Free space where downloads go is checked before anything new is downloaded.">
        <Field
          label="Keep free (GB)"
          help="Releases that would leave less than this free are not grabbed. 0 turns the check off."
        >
          <NumberInput value={form.minFreeGB} step={5} onChange={(minFreeGB) => update({ minFreeGB })} />
        </Field>
      </Section>

      <Section
        title="Notifications"
        hint="A message when something is imported, a download fails, the VPN drops, or disk space runs low."
      >
        <Field
          label="Notification URL"
          help="An ntfy topic (https://ntfy.sh/your-topic), a Discord webhook URL, or any URL that accepts a plain text POST. Empty sends none."
          group
        >
          <NotifyInput value={form.notifyURL} onChange={(notifyURL) => update({ notifyURL })} />
        </Field>
      </Section>

      <Section title="Login" hint="A password for this web interface. Any user name works.">
        {s.passwordFromEnv ? (
          <p className="muted">The password is set by the UNIFIED_PASSWORD environment variable.</p>
        ) : (
          <>
            <p className="muted small">{s.passwordSet ? 'A password is set.' : 'No password: anyone on your network can open this page.'}</p>
            <Field label={s.passwordSet ? 'New password' : 'Password'}>
              <input
                type="password"
                autoComplete="new-password"
                value={form.newPassword}
                disabled={form.removePassword}
                onChange={(e) => update({ newPassword: e.target.value })}
              />
            </Field>
            {s.passwordSet && (
              <label className="check">
                <input
                  type="checkbox"
                  checked={form.removePassword}
                  onChange={(e) => update({ removePassword: e.target.checked, newPassword: '' })}
                />
                Remove the password
              </label>
            )}
          </>
        )}
      </Section>

      <Section title="System" hint="Change these in the config file, then restart.">
        <dl className="facts">
          <dt>Config file</dt>
          <dd>
            <code>{s.fixed.configPath}</code>
          </dd>
          <dt>Downloads</dt>
          <dd>
            <code>{s.fixed.downloadDir}</code>
          </dd>
          <dt>Movies</dt>
          <dd>
            <code>{s.fixed.moviesDir}</code>
          </dd>
          <dt>TV</dt>
          <dd>
            <code>{s.fixed.tvDir}</code>
          </dd>
          <dt>Data</dt>
          <dd>
            <code>{s.fixed.dataDir}</code>
          </dd>
          <dt>Torrent port</dt>
          <dd>{s.fixed.listenPort}</dd>
          <dt>VPN interface</dt>
          <dd>
            {s.fixed.vpnInterface ? <code>{s.fixed.vpnInterface}</code> : <span className="muted">none</span>}
            {s.fixed.vpnFromEnv && <span className="muted small"> (set by the container)</span>}
          </dd>
        </dl>
      </Section>

      <div className="savebar">
        {save.error && <span className="error-text">{save.error}</span>}
        {saved && !dirty && <span className="ok">Saved</span>}
        <button className="button" disabled={!dirty || save.busy} onClick={() => setForm(toForm(s))}>
          Undo changes
        </button>
        <button className="button primary" disabled={!dirty || save.busy} onClick={submit}>
          {save.busy ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="panel">
      <h2>{title}</h2>
      {hint && <p className="muted small">{hint}</p>}
      {children}
    </section>
  )
}

/** A labelled control. Use group for several controls, since a <label>
 * forwards clicks on its text to the first control inside it. */
function Field(props: { label: string; help?: string; group?: boolean; children: ReactNode }) {
  const { label, help, group, children } = props
  const body = (
    <>
      <span className="field-label">{label}</span>
      {children}
      {help && <span className="muted small">{help}</span>}
    </>
  )
  return group ? (
    <div className="field" role="group" aria-label={label}>
      {body}
    </div>
  ) : (
    <label className="field">{body}</label>
  )
}

function SecretInput(props: { label: string; value: string; onChange: (v: string) => void }) {
  const { label, value, onChange } = props
  const [shown, setShown] = useState(false)
  return (
    <span className="secret">
      <input
        type={shown ? 'text' : 'password'}
        aria-label={label}
        value={value}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => onChange(e.target.value)}
      />
      <button type="button" className="button small" onClick={() => setShown(!shown)}>
        {shown ? 'Hide' : 'Show'}
      </button>
    </span>
  )
}

function NumberInput({ value, onChange, step = 0.5 }: { value: number; onChange: (v: number) => void; step?: number }) {
  return (
    <input
      type="number"
      min={0}
      step={step}
      value={value}
      onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
    />
  )
}

function NotifyInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const test = useAction()
  const [result, setResult] = useState<{ ok: boolean; error?: string }>()
  const run = () => {
    setResult(undefined)
    test.run(async () => setResult(await api.testNotify(value.trim())))
  }
  return (
    <>
      <span className="secret">
        <input
          aria-label="Notification URL"
          value={value}
          placeholder="https://ntfy.sh/your-topic"
          spellCheck={false}
          onChange={(e) => {
            onChange(e.target.value)
            setResult(undefined)
          }}
        />
        <button type="button" className="button small" onClick={run} disabled={test.busy || !value.trim()}>
          {test.busy ? 'Sending…' : 'Send test'}
        </button>
      </span>
      {result?.ok && <span className="ok">Sent</span>}
      {result && !result.ok && <span className="error-text">{result.error}</span>}
      {test.error && <span className="error-text">{test.error}</span>}
    </>
  )
}

function IndexerRow(props: { indexer: Indexer; onChange: (ix: Indexer) => void; onRemove: () => void }) {
  const { indexer, onChange, onRemove } = props
  const test = useAction()
  const [result, setResult] = useState<{ ok: boolean; error?: string }>()
  const run = () => {
    setResult(undefined)
    test.run(async () => setResult(await api.testIndexer(indexer)))
  }
  return (
    <div className="indexer">
      <div className="field-row">
        <Field label="Name">
          <input value={indexer.name} onChange={(e) => onChange({ ...indexer, name: e.target.value })} />
        </Field>
        <Field label="URL">
          <input
            value={indexer.url}
            placeholder="http://192.168.1.10:9696/1/api"
            onChange={(e) => onChange({ ...indexer, url: e.target.value })}
          />
        </Field>
      </div>
      <Field label="API key" group>
        <SecretInput label="API key" value={indexer.apiKey} onChange={(apiKey) => onChange({ ...indexer, apiKey })} />
      </Field>
      <div className="actions">
        <button className="button small" onClick={run} disabled={test.busy || !indexer.url}>
          {test.busy ? 'Testing…' : 'Test'}
        </button>
        <button className="button small danger" onClick={onRemove}>
          Remove
        </button>
        {result?.ok && <span className="ok">Works</span>}
        {result && !result.ok && <span className="error-text">{result.error}</span>}
        {test.error && <span className="error-text">{test.error}</span>}
      </div>
    </div>
  )
}

function ResolutionList({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const move = (i: number, by: number) => {
    const next = [...value]
    ;[next[i], next[i + by]] = [next[i + by], next[i]]
    onChange(next)
  }
  const unused = RESOLUTIONS.filter((r) => !value.includes(r))
  return (
    <div className="reslist">
      {value.map((r, i) => (
        <div className="res" key={r}>
          <span className="res-rank">{i + 1}</span>
          <span className="res-name">{r}</span>
          <button type="button" className="icon" aria-label={`Prefer ${r} more`} disabled={i === 0} onClick={() => move(i, -1)}>
            ↑
          </button>
          <button
            type="button"
            className="icon"
            aria-label={`Prefer ${r} less`}
            disabled={i === value.length - 1}
            onClick={() => move(i, 1)}
          >
            ↓
          </button>
          <button
            type="button"
            className="icon"
            aria-label={`Stop accepting ${r}`}
            disabled={value.length === 1}
            onClick={() => onChange(value.filter((x) => x !== r))}
          >
            ×
          </button>
        </div>
      ))}
      {unused.length > 0 && (
        <div className="res-add">
          {unused.map((r) => (
            <button type="button" key={r} className="button small" onClick={() => onChange([...value, r])}>
              + {r}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
