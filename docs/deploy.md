# Deploy

```sh
cp example.env .env   # fill in required values
set -a; . ./.env; set +a
GOWORK=off go run -mod=vendor .
```

Tests (`GOWORK=off go test -mod=vendor ./...`) use an in-memory store. Quadlet: `deploy/openpak-np-uploader.container`; image
`ghcr.io/openpak/np-uploader`. np-account forwards `/uploader/` here when `NP_UPLOADER_URL` is set. The console names
`uploader.<a>.<b>.community.playstation.net`; the DNS and TLS front must resolve it to np-account like the `updptl.` hosts.
