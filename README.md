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

Only traffic from the tunnel's address goes through Mullvad, and only the
torrent engine uses that address. The web UI, Prowlarr and TMDB use the normal
network, and nothing outside the container is affected. Without `wg0.conf` the
container refuses to start, unless `VPN=off` is set.

The image builds for amd64 and arm64:

    docker buildx build --platform linux/amd64,linux/arm64 -t unified .

## Layout

    main.go, run.go        CLI and the wanted -> downloading -> imported loop
    api.go                 JSON API for the web UI, and serving the built app
    ui/                    the web UI (React, TypeScript, Vite)
    Dockerfile, docker/    container image and its startup script
    internal/config        TOML config
    internal/store         SQLite library and download state
    internal/tmdb          metadata lookups
    internal/xem           scene numbering and names from TheXEM
    internal/torznab       indexer search
    internal/release       release name parsing and ranking
    internal/engine        embedded torrent client
    internal/importer      hardlink into the library
