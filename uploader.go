package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"openpak/np-common/ticket"
)

// Wire format: research note ps3/messaging-protocol.md section 4 (static RE of VSH 4.93).
// Request: <uploader><ticket/><ticketjid/>...</uploader>, then for upload "\r\n\r\n" and the raw
// bytes. Reply: <uploader result="XX">...</uploader>, for download followed by "\r\n\r\n" and
// the bytes. The reply field names come from the firmware parser; the result codes are the TUS
// set and the exact values the firmware expects are unconfirmed.
const (
	resultOK          = "00"
	resultBadTicket   = "02"
	resultForbidden   = "06"
	resultInternal    = "07"
	resultTooLarge    = "1a"
	resultNoData      = "4a"
	resultUnspecified = "ff"

	maxXML  = 64 << 10
	dateFmt = "2006-01-02T15:04:05.00Z"
	sep     = "\r\n\r\n"
)

var (
	commIDPattern = regexp.MustCompile(`^[A-Z]{4}[0-9]{5}_[0-9]{2}$`)
	refPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	quotas        = map[string]bool{"game": true, "sys": true, "premo": true, "premo2": true}
)

type Core interface {
	ticket.AccountCore
	CanInteract(context.Context, string, string) (bool, error)
}

type Service struct {
	Core    Core
	Tickets *ticket.Signer
	Store   Store
	Log     *slog.Logger
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /uploader/sec/upload_data", s.handle(s.upload))
	mux.HandleFunc("POST /uploader/sec/download_data", s.handle(s.download))
	mux.HandleFunc("POST /uploader/sec/delete_data", s.handle(s.remove))
	// Reply shapes of these two were not recovered from the firmware; answer an explicit error
	// instead of leaving the console waiting.
	for _, op := range []string{"get_data_list", "get_quota"} {
		mux.HandleFunc("POST /uploader/sec/"+op, s.handle(func(c *call) { reply(c.w, resultUnspecified, "", nil) }))
	}
}

type request struct {
	XMLName     xml.Name `xml:"uploader"`
	Ticket      string   `xml:"ticket"`
	TicketJID   string   `xml:"ticketjid"`
	Quota       string   `xml:"quota"`
	Application string   `xml:"application"`
	Size        int      `xml:"size"`
	Reference   string   `xml:"reference"`
}

type call struct {
	w     http.ResponseWriter
	r     *http.Request
	req   request
	actor ticket.Identity
	jid   string
	data  []byte
}

func (s *Service) handle(h func(*call)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &call{w: w, r: r}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxXML+MaxObject+4))
		if err != nil {
			reply(w, resultTooLarge, "", nil)
			return
		}
		head := body
		if i := bytes.Index(body, []byte(sep)); i >= 0 {
			head, c.data = body[:i], body[i+len(sep):]
		}
		if len(head) > maxXML || xml.Unmarshal(head, &c.req) != nil {
			reply(w, resultUnspecified, "", nil)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(c.req.Ticket), ""))
		if err == nil {
			c.actor, err = ticket.VerifyAccount(r.Context(), s.Core, s.Tickets, raw)
		}
		if err != nil {
			reply(w, resultBadTicket, "", nil)
			return
		}
		c.jid = s.jid(r.Context(), c.actor)
		h(c)
	}
}

func (s *Service) jid(ctx context.Context, id ticket.Identity) string {
	country := "us"
	if a, err := s.Core.GetAccount(ctx, id.AccountID); err == nil && len(a.Country) == 2 {
		country = strings.ToLower(a.Country)
	}
	return id.OnlineID + "@" + country + ".np.community.playstation.net"
}

func (s *Service) upload(c *call) {
	q := c.req.Quota
	if !quotas[q] || !commIDPattern.MatchString(c.req.Application) || c.req.Size != len(c.data) || len(c.data) == 0 {
		reply(c.w, resultUnspecified, "", nil)
		return
	}
	if len(c.data) > MaxObject {
		reply(c.w, resultTooLarge, "", nil)
		return
	}
	var b [16]byte
	rand.Read(b[:])
	o := Object{Ref: hex.EncodeToString(b[:]), Owner: c.actor.AccountID, OwnerJID: c.jid, Application: c.req.Application,
		Quota: q, Date: time.Now().UTC(), Body: c.data}
	switch err := s.Store.Put(c.r.Context(), o); {
	case errors.Is(err, ErrFull):
		reply(c.w, resultTooLarge, "", nil)
	case err != nil:
		s.Log.Warn("uploader store", "err", err)
		reply(c.w, resultInternal, "", nil)
	default:
		reply(c.w, resultOK, `<reference>`+o.Ref+`</reference>`, nil)
	}
}

// fetch loads the object for a download or delete request.
func (s *Service) fetch(c *call) (Object, bool) {
	if !refPattern.MatchString(c.req.Reference) {
		reply(c.w, resultUnspecified, "", nil)
		return Object{}, false
	}
	o, err := s.Store.Get(c.r.Context(), c.req.Reference)
	if err != nil {
		code := resultNoData
		if !errors.Is(err, ErrNotFound) {
			s.Log.Warn("uploader store", "err", err)
			code = resultInternal
		}
		reply(c.w, code, "", nil)
		return Object{}, false
	}
	return o, true
}

// download is allowed to the owner and to anyone who may interact with the owner (not blocked
// either way); the reference is unguessable and only travels inside a message to a friend.
func (s *Service) download(c *call) {
	o, ok := s.fetch(c)
	if !ok {
		return
	}
	if o.Owner != c.actor.AccountID {
		allowed, err := s.Core.CanInteract(c.r.Context(), c.actor.AccountID, o.Owner)
		if err != nil {
			reply(c.w, resultInternal, "", nil)
			return
		}
		if !allowed {
			reply(c.w, resultForbidden, "", nil)
			return
		}
	}
	reply(c.w, resultOK, `<jid>`+esc(o.OwnerJID)+`</jid><quota>`+o.Quota+`</quota><application>`+o.Application+
		`</application><size>`+strconv.Itoa(len(o.Body))+`</size><date>`+o.Date.Format(dateFmt)+`</date>`, o.Body)
}

func (s *Service) remove(c *call) {
	if !refPattern.MatchString(c.req.Reference) {
		reply(c.w, resultUnspecified, "", nil)
		return
	}
	switch err := s.Store.Delete(c.r.Context(), c.actor.AccountID, c.req.Reference); {
	case errors.Is(err, ErrNotFound):
		reply(c.w, resultNoData, "", nil)
	case err != nil:
		s.Log.Warn("uploader store", "err", err)
		reply(c.w, resultInternal, "", nil)
	default:
		reply(c.w, resultOK, "", nil)
	}
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// reply always answers HTTP 200 with a Content-Length, like the other NP services.
func reply(w http.ResponseWriter, result, inner string, data []byte) {
	b := []byte(`<?xml version="1.0" encoding="utf-8"?><uploader result="` + result + `">` + inner + `</uploader>`)
	if data != nil {
		b = append(append(b, sep...), data...)
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
