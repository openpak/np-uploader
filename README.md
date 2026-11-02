# PS3 Uploader Server

OpenPak's PS3 attachment store: the firmware's `uploader` service that holds data too large for a message stanza.

| | |
| --- | --- |
| Network | PlayStation 3 |
| Role | `/uploader/sec/upload_data`, `download_data`, `delete_data` for message attachments (`inline="fileid"`) |
| Depends on | [`account`](https://github.com/openpak/account) (account core), [`np-common`](https://github.com/openpak/np-common) (vendored), PostgreSQL |
| Used by | [`np-account`](https://github.com/openpak/np-account) (forwards `/uploader/` via `NP_UPLOADER_URL`) |
| Image | `ghcr.io/openpak/np-uploader` |
| Licence | AGPL-3.0-only |

The wire format is static RE of firmware 4.93 and has not been confirmed against a console; see [docs/protocol.md](docs/protocol.md).

## Run

```sh
cp example.env .env   # fill in required values
set -a; . ./.env; set +a
GOWORK=off go run -mod=vendor .
```
