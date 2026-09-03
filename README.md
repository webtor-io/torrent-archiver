# torrent-archiver
Generates zip/tar archive from torrent. The format is selected by the
extension of the requested filename (`<name>.zip` or `<name>.tar`, zip by
default). Both formats are laid out deterministically (zip uses Store, tar is
plain ustar/PAX), so archive size is known up front and any byte range can be
served independently — downloads are resumable. Tar additionally carries no
per-file checksums, so a resumed download never unpacks as "corrupt".

By default the archive contains every file under the requested path
(`X-Origin-Path`). A partial archive can be requested with one or more
repeated `?paths=` query values: each value is a torrent-rooted file or
folder path, and only files matching a value exactly or living under a
value-as-folder are included. The selection participates in the `ETag`, a
selection matching no files yields `404`, and more than 1024 values yield
`400`.

## Streaming behaviour

The archive is a strictly sequential stream, but upstream reads are not: while
the current file streams to the client, the next `--prefetch-depth` files
(default 4) that are small enough (`--prefetch-max-file-bytes`, default 8 MiB)
are fetched into memory, within `--prefetch-budget-bytes` (default 64 MiB) per
request. Big files are never prefetched — they stream as before, and
torrent-http-proxy caps distinct big files per session at five, a budget the
user's own player shares. `PREFETCH_DEPTH=0` turns prefetching off.

Before blocking on an upstream body the writer flushes everything it has —
the entry's header included — so a slow swarm never looks like a silent
connection to the client. Neither mechanism outlives the request: a client
that disconnects cancels its prefetches; keeping the swarm working across a
client's retries is torrent-web-seeder's job (`READER_LINGER`).

# Usage

```
% ./torrent-archiver --help
NAME:
   torrent-archiver - Generates archive with selected content from torrent

USAGE:
   torrent-archiver [global options] command [command options] [arguments...]

VERSION:
   0.0.1

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --probe-host value          probe listening host
   --probe-port value          probe listening port (default: 8081)
   --host value                listening host
   --port value                http listening port (default: 8080)
   --torrent-store-host value  torrent store host [$TORRENT_STORE_SERVICE_HOST, $ TORRENT_STORE_HOST]
   --torrent-store-port value  torrent store port (default: 50051) [$TORRENT_STORE_SERVICE_PORT, $ TORRENT_STORE_PORT]
   --help, -h                  show help
   --version, -v               print the version
```
