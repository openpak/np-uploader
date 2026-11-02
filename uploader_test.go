package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/identity"
	"openpak/np-common/ticket"
)

const commid = "NPWR01234_00"

type user struct {
	id  string
	raw []byte
	jid string
}

func setup(t *testing.T, store Store) (*httptest.Server, map[string]user) {
	const coreKey = "test-core-key-at-least-thirty-two-bytes"
	fake := fakecore.New(coreKey)
	fake.AddAccount(fakecore.Account{ID: "alice", DisplayName: "Alice", Country: "PT"})
	fake.AddAccount(fakecore.Account{ID: "bob", DisplayName: "Bobby", Country: "US"})
	fake.AddAccount(fakecore.Account{ID: "carol", DisplayName: "Carol", Country: "US"})
	fake.AddFriend("alice", "bob")
	fake.SetBlocked("carol", "alice", true)
	core, err := coreclient.Dial("passthrough:///core", coreKey, fake.Serve(t)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { core.Close() })
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ticket.New(key, nil)
	users := map[string]user{}
	for _, id := range []string{"alice", "bob", "carol"} {
		acct, _ := core.GetAccount(context.Background(), id)
		l, err := identity.EnsureNPLink(context.Background(), core, acct)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := signer.Issue(ticket.Identity{AccountID: id, LinkID: l.LinkID, OnlineID: l.OnlineID, Country: "us", Service: "IV0001-NPXS01001_00"}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		users[id] = user{id, raw, l.OnlineID + "@us.np.community.playstation.net"}
	}
	mux := http.NewServeMux()
	(&Service{Core: core, Tickets: signer, Store: store, Log: slog.Default()}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users
}

func call_(t *testing.T, srv *httptest.Server, op string, u user, inner string, data []byte) string {
	t.Helper()
	body := `<?xml version="1.0" encoding="utf-8"?><uploader platform="ps3" sv="4.93"><ticket>` + base64.StdEncoding.EncodeToString(u.raw) +
		`</ticket><ticketjid>` + u.jid + `</ticketjid>` + inner + `</uploader>`
	b := []byte(body)
	if data != nil {
		b = append(append(b, sep...), data...)
	}
	resp, err := http.Post(srv.URL+"/uploader/sec/"+op, "application/octet-stream", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Length") != strconv.Itoa(len(out)) {
		t.Fatalf("%s: HTTP %d, Content-Length %q for %d bytes", op, resp.StatusCode, resp.Header.Get("Content-Length"), len(out))
	}
	return string(out)
}

func TestUploadDownloadDelete(t *testing.T) {
	srv, u := setup(t, &MemStore{})
	payload := []byte("\x00\x01binary\r\n\r\ndata")
	up := `<quota>game</quota><application>` + commid + `</application><size>` + strconv.Itoa(len(payload)) + `</size>`
	r := call_(t, srv, "upload_data", u["alice"], up, payload)
	i := strings.Index(r, "<reference>")
	if !strings.Contains(r, `<uploader result="00">`) || i < 0 {
		t.Fatal(r)
	}
	ref := r[i+len("<reference>") : i+len("<reference>")+32]
	get := `<reference>` + ref + `</reference>`

	// The friend and the owner can download; the byte stream follows the XML after CRLFCRLF.
	for _, who := range []string{"alice", "bob"} {
		r = call_(t, srv, "download_data", u[who], get, nil)
		j := strings.Index(r, sep)
		if j < 0 || r[j+len(sep):] != string(payload) || !strings.Contains(r[:j], `<size>`+strconv.Itoa(len(payload))+`</size>`) ||
			!strings.Contains(r[:j], `<jid>Alice@pt.np.community.playstation.net</jid>`) || !strings.Contains(r[:j], `<application>`+commid+`</application>`) {
			t.Fatalf("%s download: %q", who, r)
		}
	}
	// Carol blocked Alice: forbidden.
	if r = call_(t, srv, "download_data", u["carol"], get, nil); !strings.Contains(r, `result="06"`) {
		t.Fatal(r)
	}
	// Only the owner deletes.
	if r = call_(t, srv, "delete_data", u["bob"], get, nil); !strings.Contains(r, `result="4a"`) {
		t.Fatal(r)
	}
	if r = call_(t, srv, "delete_data", u["alice"], get, nil); !strings.Contains(r, `result="00"`) {
		t.Fatal(r)
	}
	if r = call_(t, srv, "download_data", u["alice"], get, nil); !strings.Contains(r, `result="4a"`) {
		t.Fatal(r)
	}
}

func TestUploadValidation(t *testing.T) {
	srv, u := setup(t, &MemStore{})
	bad := u["alice"]
	bad.raw = []byte("AAAA")
	for name, c := range map[string]struct {
		who   user
		inner string
		data  []byte
		want  string
	}{
		"bad ticket":    {bad, `<quota>game</quota><application>` + commid + `</application><size>1</size>`, []byte("x"), "02"},
		"size mismatch": {u["alice"], `<quota>game</quota><application>` + commid + `</application><size>5</size>`, []byte("x"), "ff"},
		"bad quota":     {u["alice"], `<quota>x</quota><application>` + commid + `</application><size>1</size>`, []byte("x"), "ff"},
		"bad commid":    {u["alice"], `<quota>game</quota><application>nope</application><size>1</size>`, []byte("x"), "ff"},
		"too large":     {u["alice"], `<quota>game</quota><application>` + commid + `</application><size>` + strconv.Itoa(MaxObject+1) + `</size>`, make([]byte, MaxObject+1), "1a"},
		"unknown ref":   {u["alice"], `<reference>` + strings.Repeat("0", 32) + `</reference>`, nil, ""},
	} {
		op := "upload_data"
		if c.want == "" {
			op, c.want = "download_data", "4a"
		}
		if r := call_(t, srv, op, c.who, c.inner, c.data); !strings.Contains(r, `result="`+c.want+`"`) {
			t.Errorf("%s: want %s got %s", name, c.want, r)
		}
	}
}

func TestOwnerQuota(t *testing.T) {
	s := &MemStore{}
	ctx := context.Background()
	for i := 0; i < MaxItems; i++ {
		if err := s.Put(ctx, Object{Ref: strconv.Itoa(i), Owner: "a", Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, Object{Ref: "over", Owner: "a", Body: []byte("x")}); err != ErrFull {
		t.Fatal("item cap not enforced:", err)
	}
	if err := s.Put(ctx, Object{Ref: "other", Owner: "b", Body: make([]byte, MaxPerOwner+1)}); err != ErrFull {
		t.Fatal("byte cap not enforced:", err)
	}
}
