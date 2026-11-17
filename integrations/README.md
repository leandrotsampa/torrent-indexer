# Integrations

Ready-to-use files to plug torrent-indexer into the *arr stack:

| Path | What it is |
|---|---|
| [`prowlarr/`](prowlarr/) | One Cardigann (YML) definition per indexer, for Prowlarr's custom definitions |
| [`custom-formats/`](custom-formats/) | Custom formats to import into Sonarr/Radarr |

There are two ways to add the indexers to Prowlarr. **Prefer Generic Torznab**:
it is the only one that hands the audio languages to Sonarr/Radarr.

| | Generic Torznab (recommended) | YML definitions |
|---|---|---|
| Files to copy | none | `prowlarr/*.yml` |
| Audio languages (`<torznab:attr name="language">`) | ✅ | ❌ ([Prowlarr#2550](https://github.com/Prowlarr/Prowlarr/issues/2550)) |
| `(brazilian, eng)` / `(eng, legendado)` title tags | ✅ | ✅ |
| `[torrent-indexer]` title tag | ❌ | ✅ |

Pick one per site: adding the same site both ways makes every release show up
twice in Sonarr/Radarr.

The examples use `http://torrent-indexer:7006/`, which resolves when Prowlarr
shares a Docker network with torrent-indexer (see the `indexer` network in
[`docker-compose.yml`](../docker-compose.yml); `docker network connect indexer prowlarr`
does it for an existing container). Otherwise use `http://<host>:<port>/` with the
port published by your compose file.

## Generic Torznab

1. *Settings → Indexers → Add Indexer → **Generic Torznab***.
2. **Url** — one indexer per URL, no trailing slash:

   | Indexer | Url |
   |---|---|
   | BluDV | `http://torrent-indexer:7006/torznab/bludv` |
   | Comando Torrents | `http://torrent-indexer:7006/torznab/comando_torrents` |
   | Rede Torrent | `http://torrent-indexer:7006/torznab/rede_torrent` |
   | Starck Filmes | `http://torrent-indexer:7006/torznab/starck-filmes` |
   | Torrent dos Filmes | `http://torrent-indexer:7006/torznab/torrent-dos-filmes` |
   | Vaca Torrent | `http://torrent-indexer:7006/torznab/vaca_torrent` |
   | Darkmahou (anime) | `http://torrent-indexer:7006/torznab/darkmahou` |
   | Erai-raws (anime) | `http://torrent-indexer:7006/torznab/erai_raws` |
   | Nyaa (anime) | `http://torrent-indexer:7006/torznab/nyaa` |

3. **Api Path**: keep the default `/api` (the suffix is accepted) or clear it.
4. **Api Key**: empty. The categories come from `t=caps`.
5. Name it after its origin (e.g. *BluDV (torrent-indexer)*), **Test** and save.

The Torznab endpoint already applies the search tweaks the YMLs do with
`keywordsfilters` (see [below](#search-tweaks)), so there is nothing else to set.

To check what Prowlarr receives:

```bash
curl "http://<host>:7006/torznab/bludv?t=tvsearch&q=supergirl&season=1&limit=3"
```

```xml
<title>Supergirl S01 1ª Temporada Torrent – BluRay 720p Dual Áudio (brazilian, eng)</title>
<torznab:attr name="language" value="Portuguese (Brazil)"/>
<torznab:attr name="language" value="English"/>
```

Sonarr/Radarr then filter on the language wherever they already do: *Quality
Profile → Language*, the *Language* condition of a custom format, or the language
column of the interactive search.

## YML definitions

1. Copy [`prowlarr/*.yml`](prowlarr/) into Prowlarr's custom definitions folder
   (create `Custom` if it does not exist):
   - Docker: `<prowlarr-config>/Definitions/Custom/`
   - Windows: `C:\ProgramData\Prowlarr\Definitions\Custom\`
   - Linux: `/var/lib/prowlarr/Definitions/Custom/` (or `~/.config/Prowlarr/Definitions/Custom/`)
2. Restart Prowlarr — custom definitions are only read at startup.
3. *Settings → Indexers → Add Indexer*, search for the name (all end in
   *(torrent-indexer)*) and add it.
4. Check the **Base Url** (default `http://torrent-indexer:7006/`), **Test** and save.

| File | Indexer | Categories |
|---|---|---|
| `torrent-indexer-bludv.yml` | BluDV | Movies, TV |
| `torrent-indexer-comando.yml` | Comando Torrents | Movies, TV |
| `torrent-indexer-rede-torrent.yml` | Rede Torrent | Movies, TV |
| `torrent-indexer-starck-filmes.yml` | Starck Filmes | Movies, TV |
| `torrent-indexer-torrent-dos-filmes.yml` | Torrent dos Filmes | Movies, TV |
| `torrent-indexer-vaca-torrent.yml` | Vaca Torrent | Movies, TV |
| `torrent-indexer-darkmahou.yml` | Darkmahou | TV/Anime, Movies |
| `torrent-indexer-erai-raws.yml` | Erai-raws | TV/Anime, Movies |
| `torrent-indexer-nyaa.yml` | Nyaa | TV/Anime, Movies |

The release title is the API `title` field — the same one the Torznab feed uses —
followed by ` [torrent-indexer]`. A definition's `id` is how Prowlarr ties a
configured indexer to its file, so keep the ids when editing these files.

## Title tags and custom formats

Every release title ends with the audio languages torrent-indexer detected, and
with `legendado` when the release has no Portuguese audio (see
[Release title tags](../README.md#release-title-tags); for Nyaa, `legendado`
means Portuguese subtitles, read from the release page):

```
Superman 2025 1080p WEB-DL DUAL (brazilian, eng)
Superman 2025 1080p WEB-DL (eng, legendado)
```

Import a file from [`custom-formats/`](custom-formats/) in *Sonarr/Radarr →
Settings → Custom Formats → + → Import*, then score it in *Settings → Profiles*
(positive to prefer, negative to avoid):

| File | Matches | Works with |
|---|---|---|
| `legendado.json` | releases tagged `legendado` | Torznab and YML |
| `torrent-indexer.json` | any release from these indexers (`[torrent-indexer]`) | YML only |

With Generic Torznab there is no `[torrent-indexer]` tag; to tell the origin apart,
use the indexer name or a per-indexer release profile.

## Search tweaks

Both paths rewrite the search terms so that Sonarr's queries match how the
Brazilian sites name things:

- **Movie/TV sites**: `and|es|y|et → e` (*Rick and Morty* finds *Rick e Morty*)
  and `S01E02`/`S01 → temporada 1`.
- **Anime sites** (darkmahou, erai_raws): the season/episode markers are dropped,
  so the search runs on the anime name and Sonarr picks the episode by title.
- **Nyaa**: one release per episode, so a season+episode search asks for
  `SxxEyy` (absolute numbers such as `One Piece 1100` pass through untouched).

## Notes

- Do **not** attach a FlareSolverr/Byparr proxy or tag to these indexers in Prowlarr:
  torrent-indexer talks to FlareSolverr or Byparr itself through `FLARESOLVERR_ADDRESS`, and
  an Indexer Proxy would solve every challenge twice.
- When a site changes domain, update torrent-indexer (`INDEXER_<NAME>_URL`), not
  these files.
- `erai_raws` needs an Erai-raws account token (`ERAI_RAWS_AUTH_TOKEN` or
  `INDEXER_ERAI_RAWS_RSS_URL`); without it every search fails with an
  authentication error.
- Subtitle *tracks* are not sent as the torznab `subs` attribute: Prowlarr does not
  forward it and Sonarr/Radarr do not read it
  ([Sonarr#7625](https://github.com/Sonarr/Sonarr/issues/7625)). The `legendado`
  title tag covers the dubbed × subtitled choice instead.
