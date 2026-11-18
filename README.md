# unified

One program that finds, downloads and files movies and TV episodes:
a library, Torznab search, release ranking, an embedded BitTorrent client
and a hardlinking importer, with a terminal interface and a web UI.

## Web UI

    ./unified serve                      # http://127.0.0.1:8080
    ./unified serve --addr 0.0.0.0:8080  # reachable from other devices; set password in the config

`serve` keeps running: it downloads, imports and seeds, and searches again
every 30 minutes for wanted items still missing (including newly aired
episodes). Don't run `unified run` at the same time.

The UI has the library with posters, a page per movie or show, search, the
releases found for anything (Grab downloads one of them yourself, even one
the ranking rejected), downloads with progress, and settings.

The Storage tab shows what the download, movie and TV folders take, their
largest items, and the free space on their disks.
Nothing new is grabbed when it would leave less than `min_free_gb` free (20 GB
by default) where downloads go. With `notify_url` set (an ntfy topic, a
Discord webhook, or any URL that takes a plain text POST), unified sends a
message when something is imported, a download fails, the VPN drops, or disk
space runs low.

Finished downloads keep seeding until they have uploaded `seed_ratio` times
their size (0: no limit), or until unified stops; seeding does not resume
after a restart. Indexers,
the TMDB key, quality rules and the password can be changed there and apply
at once; saving rewrites the config file (without its comments).

## Build

The web UI is a React app in `ui/`, built into the binary, so build it first:

    pnpm -C ui install
    pnpm -C ui build
    go build -o unified .

While working on the UI, run `./unified serve` and `pnpm -C ui dev`, and open
the address Vite prints; API calls are forwarded to the running server.

## Use

    ./unified init                       # writes ~/.config/unified/config.toml, then edit it
    ./unified add movie blade runner 2049
    ./unified add show severance -s 2    # -s season, -e episode; without them, everything
    ./unified list                       # library; `unified list <id>` for a show's episodes
    ./unified search 1                   # see how releases are ranked, grabs nothing
    ./unified run                        # search, download and import everything wanted

`unified run` exits when all downloads are imported; `unified run --seed` keeps
seeding until Ctrl-C. Interrupted downloads resume on the next run.

## Sharing the network

Torrents can make the rest of a home network slow, mostly through a full
upload: the router's queues fill up and everyone's traffic waits behind the
torrents' (bufferbloat). unified keeps this down in a few ways:

- Outgoing peer connections prefer uTP, which backs off as soon as it sees
  delay building up. TCP waits two seconds for uTP to connect and is only
  used for peers without uTP. Incoming TCP is still accepted. The downloads
  page shows how many connections use each.
- `max_download_kbps` and `max_upload_kbps` limit all torrents together, in
  KiB/s (1000 KiB/s is about 8 Mbit/s); 0 is no limit. An upload limit
  somewhat below your connection's upload speed helps the most.
  `alt_download_kbps` and `alt_upload_kbps` apply instead during
  `alt_schedule`, a daily window such as `"08:00-23:00"` (`"22:00-06:00"`
  runs past midnight). Changes in the settings apply at once.
- Only `max_active_downloads` torrents download at once and only
  `max_active_seeds` seed (3 each by default; 0 is no limit). The rest wait
  their turn, shown as queued; a queued download isn't added to the torrent
  client yet, so it makes no connections. Interrupted downloads go back in
  line on the next start.
- At most 30 connections per torrent, and `max_connections` (200 by default)
  shared among the running ones, so a home router's connection table doesn't
  fill up.

These help, but the real fix for bufferbloat is on the router: turn on SQM
(Smart Queue Management) with fq_codel or CAKE, as OpenWrt, OPNsense,
pfSense and many consumer routers offer, and set it a little below your
connection's real speeds. Then a full upload no longer slows everything
else down, whatever program causes it.

## VPN

Set `vpn_interface` in the config (the Mullvad app on Linux uses
`wg0-mullvad`) and all torrent traffic is bound to that interface: peers, DHT,
trackers and web seeds. If the interface is missing at startup, unified refuses to
start. If it goes down while running, traffic stops rather than using the
regular connection; torrents are paused and the web UI shows it. Searches and
metadata lookups don't use the VPN.

## Container

    docker compose up -d --build

See `compose.yaml`. Before starting:

- Put a Mullvad WireGuard config at `config/wg0.conf`. Use a separate
  Mullvad device (key) from the one on your PC: one key connected from two
  places at once makes both connections unreliable.
- Point the `/media` volume at a folder that holds downloads, movies and tv,
  writable by `PUID`/`PGID`.

On first start a `config/config.toml` is created; add your TMDB key, indexers
and optionally a password, then `docker compose restart`. The UI is on port
8080.

Prowlarr runs alongside it, on port 9696, with its data in `prowlarr/`. Add
trackers there, then add each one to unified as an indexer with the url
`http://prowlarr:9696/<id>/api` and Prowlarr's API key.

Only traffic from the tunnel's address goes through Mullvad, and only the
torrent engine uses that address. The web UI, Prowlarr and TMDB use the normal
network, and nothing outside the container is affected. Without `wg0.conf` the
container refuses to start, unless `VPN=off` is set.

The image builds for amd64 and arm64:

    docker buildx build --platform linux/amd64,linux/arm64 -t unified .

## Layout

    main.go, run.go        CLI and the wanted -> downloading -> imported loop
    queue.go               download and seeding slots, speed limits, connections
    indexers.go            backing off indexers that fail
    api.go                 JSON API for the web UI, and serving the built app
    ui/                    the web UI (React, TypeScript, Vite)
    Dockerfile, docker/    container image and its startup script
    internal/config        TOML config
    internal/store         SQLite library and download state
    internal/tmdb          metadata lookups
    internal/xem           scene numbering and names from TheXEM
    internal/torznab       indexer search
    internal/release       release name parsing and ranking
    internal/engine        embedded torrent client; prefers uTP over TCP
    internal/importer      hardlink into the library
    internal/disk          free space and folder sizes
    internal/notify        notifications (ntfy, Discord, plain POST)
