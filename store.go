package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Object is one uploaded attachment.
type Object struct {
	Ref         string
	Owner       string // core account id
	OwnerJID    string
	Application string // communication id
	Quota       string // game | sys | premo | premo2
	Date        time.Time
	Body        []byte
	Size        int // set by List, where Body is not loaded
}

var (
	ErrNotFound = errors.New("no such object")
	ErrFull     = errors.New("owner quota exceeded")
)

const (
	MaxObject   = 1 << 20  // one attachment; Sony's limit is unknown
	MaxPerOwner = 16 << 20 // all of one account's attachments
	MaxItems    = 200
)

type Store interface {
	// Put stores o unless the owner would exceed MaxPerOwner or MaxItems.
	Put(ctx context.Context, o Object) error
	Get(ctx context.Context, ref string) (Object, error)
	// Delete removes ref when owned by owner.
	Delete(ctx context.Context, owner, ref string) error
	// List returns the owner's objects (Body left empty, Size in len via Size field), oldest first.
	List(ctx context.Context, owner string) ([]Object, error)
}

type MemStore struct {
	mu sync.Mutex
	m  map[string]Object
}

func (s *MemStore) Put(_ context.Context, o Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]Object{}
	}
	n, total := 0, 0
	for _, x := range s.m {
		if x.Owner == o.Owner {
			n++
			total += len(x.Body)
		}
	}
	if n >= MaxItems || total+len(o.Body) > MaxPerOwner {
		return ErrFull
	}
	s.m[o.Ref] = o
	return nil
}

func (s *MemStore) Get(_ context.Context, ref string) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.m[ref]; ok {
		return o, nil
	}
	return Object{}, ErrNotFound
}

func (s *MemStore) Delete(_ context.Context, owner, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.m[ref]; !ok || o.Owner != owner {
		return ErrNotFound
	}
	delete(s.m, ref)
	return nil
}

func (s *MemStore) List(_ context.Context, owner string) ([]Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Object
	for _, o := range s.m {
		if o.Owner == owner {
			o.Size, o.Body = len(o.Body), nil
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Date.Before(out[j].Date) || out[i].Date.Equal(out[j].Date) && out[i].Ref < out[j].Ref
	})
	return out, nil
}

type PGStore struct{ DB *pgxpool.Pool }

func (s *PGStore) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS uploader_data (
		ref         text        PRIMARY KEY,
		owner       text        NOT NULL,
		owner_jid   text        NOT NULL,
		application text        NOT NULL,
		quota       text        NOT NULL,
		date        timestamptz NOT NULL,
		body        bytea       NOT NULL);
	CREATE INDEX IF NOT EXISTS uploader_data_owner ON uploader_data (owner)`)
	return err
}

func (s *PGStore) Put(ctx context.Context, o Object) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, o.Owner); err != nil {
		return err
	}
	var n, total int64
	if err = tx.QueryRow(ctx, `SELECT count(*), coalesce(sum(length(body)),0) FROM uploader_data WHERE owner = $1`, o.Owner).Scan(&n, &total); err != nil {
		return err
	}
	if n >= MaxItems || total+int64(len(o.Body)) > MaxPerOwner {
		return ErrFull
	}
	if _, err = tx.Exec(ctx, `INSERT INTO uploader_data VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.Ref, o.Owner, o.OwnerJID, o.Application, o.Quota, o.Date, o.Body); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGStore) Get(ctx context.Context, ref string) (Object, error) {
	o := Object{Ref: ref}
	err := s.DB.QueryRow(ctx, `SELECT owner, owner_jid, application, quota, date, body FROM uploader_data WHERE ref = $1`, ref).
		Scan(&o.Owner, &o.OwnerJID, &o.Application, &o.Quota, &o.Date, &o.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return Object{}, ErrNotFound
	}
	return o, err
}

func (s *PGStore) Delete(ctx context.Context, owner, ref string) error {
	t, err := s.DB.Exec(ctx, `DELETE FROM uploader_data WHERE owner = $1 AND ref = $2`, owner, ref)
	if err == nil && t.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *PGStore) List(ctx context.Context, owner string) ([]Object, error) {
	rows, err := s.DB.Query(ctx, `SELECT ref, owner_jid, application, quota, date, length(body) FROM uploader_data WHERE owner = $1 ORDER BY date, ref`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		o := Object{Owner: owner}
		if err := rows.Scan(&o.Ref, &o.OwnerJID, &o.Application, &o.Quota, &o.Date, &o.Size); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
