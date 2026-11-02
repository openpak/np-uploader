# Protocol: np-uploader

Host the console uses: `uploader.<a>.<b>.community.playstation.net` (HTTPS, via np-account's TLS front). Requests are `POST`.

Request body: `<uploader platform="ps3" sv="..."><ticket>base64 NP ticket</ticket><ticketjid>jid</ticketjid>FIELDS</uploader>`.
`upload_data` appends `\r\n\r\n` and the raw bytes.

| Path | Fields | Reply |
| --- | --- | --- |
| `/uploader/sec/upload_data` | `quota` (game, sys, premo, premo2), `application` (communication ID), `size` (must equal the byte count) | `<uploader result="00"><reference>32 hex</reference></uploader>` |
| `/uploader/sec/download_data` | `reference` | `<uploader result="00"><jid/><quota/><application/><size/><date/></uploader>` then `\r\n\r\n` and the bytes |
| `/uploader/sec/delete_data` | `reference` | `<uploader result="00"/>` |
| `/uploader/sec/get_data_list`, `get_quota` | | `result="ff"`: reply shapes unknown |

Result codes reuse the TUS set: `02` bad ticket, `06` not allowed, `07` internal, `1a` too large or owner over quota, `4a` no such
data, `ff` malformed. Replies are always HTTP 200 with `Content-Length`.

Limits (OpenPak's choice): 1 MiB per attachment, 16 MiB and 200 attachments per account. The owner downloads and deletes; anyone the
core allows to interact with the owner (no block either way) may download by reference. No expiry yet.

Request shape: recovered from VSH (`FUN_00204230`, `FUN_002045c4`). Reply field names: recovered from the firmware parsers. Result codes,
reference format and the download host: assumptions.
