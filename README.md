# torrent-archiver
Generates zip-archive from torrent

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
