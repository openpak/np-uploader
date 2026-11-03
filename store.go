package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "openpak/np-uploader/internal/titlestoragepb"
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
	// List returns the owner's objects (Body left empty, Size set), oldest first.
	List(ctx context.Context, owner string) ([]Object, error)
}

// The attachments live in the neutral title-storage core under a reserved neutral title key (no
// network: the key is not a PS3 communication id), collection "attachments", named by the
// reference, owned by the uploader. Rows are public in the core because the reference is the
// capability and this service applies the account core's interaction check on download; the quota
// (MaxPerOwner bytes, MaxItems objects, MaxObject per body) is the title's policy there.
const (
	titleKey   = "np-messages"
	collection = "attachments"
)

// CoreStore keeps attachments in the title-storage service.
type CoreStore struct{ C pb.TitleStorageClient }

// DialStorage connects to the neutral title-storage service (gRPC) and presents the internal
// key on every call. extra lets tests add a dialer.
func DialStorage(addr, key string, extra ...grpc.DialOption) (pb.TitleStorageClient, *grpc.ClientConn, error) {
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, inv grpc.UnaryInvoker, o ...grpc.CallOption) error {
			return inv(metadata.AppendToOutgoingContext(ctx, "x-internal-key", key), method, req, reply, cc, o...)
		}),
	}, extra...)
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, nil, err
	}
	return pb.NewTitleStorageClient(conn), conn, nil
}

var titleRef = &pb.TitleRef{Title: titleKey}

// Sync declares the reserved title and its limits to the core. Put works without it (the core's
// defaults apply), so a core that is down at start only delays the stricter limits.
func (s *CoreStore) Sync(ctx context.Context) error {
	_, err := s.C.UpsertTitle(ctx, &pb.UpsertTitleRequest{Title: titleKey, Name: "PS3 message attachments", QuotaBytes: MaxPerOwner,
		QuotaObjects: MaxItems, MaxObjectBytes: MaxObject, DefaultVisibility: pb.Visibility_VISIBILITY_PUBLIC})
	return err
}

type meta struct {
	JID         string `json:"jid"`
	Application string `json:"application"`
	Quota       string `json:"quota"`
}

func fromCore(o *pb.Object) Object {
	var m meta
	_ = json.Unmarshal([]byte(o.MetaJson), &m)
	return Object{Ref: o.Name, Owner: o.Owner, OwnerJID: m.JID, Application: m.Application, Quota: m.Quota,
		Date: time.UnixMilli(o.CreatedUnixMs).UTC(), Body: o.Body, Size: int(o.Size)}
}

func (s *CoreStore) Put(ctx context.Context, o Object) error {
	mj, _ := json.Marshal(meta{o.OwnerJID, o.Application, o.Quota})
	_, err := s.C.PutObject(ctx, &pb.PutObjectRequest{Ref: titleRef, Owner: o.Owner, Writer: o.Owner, Setter: o.OwnerJID, Collection: collection,
		Name: o.Ref, MetaJson: string(mj), Body: o.Body, Visibility: pb.Visibility_VISIBILITY_PUBLIC})
	if status.Code(err) == codes.ResourceExhausted {
		return ErrFull
	}
	return err
}

func (s *CoreStore) Get(ctx context.Context, ref string) (Object, error) {
	// No owner: the reference is unguessable and names the object across owners.
	r, err := s.C.GetObject(ctx, &pb.GetObjectRequest{Ref: titleRef, Collection: collection, Name: ref, IncludeBody: true})
	if err != nil {
		return Object{}, err
	}
	if !r.Found {
		return Object{}, ErrNotFound
	}
	return fromCore(r.Object), nil
}

func (s *CoreStore) Delete(ctx context.Context, owner, ref string) error {
	r, err := s.C.DeleteObjects(ctx, &pb.DeleteObjectsRequest{Ref: titleRef, Owner: owner, Writer: owner, Collection: collection, Names: []string{ref}})
	if err == nil && r.Deleted == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *CoreStore) List(ctx context.Context, owner string) ([]Object, error) {
	r, err := s.C.ListObjects(ctx, &pb.ListObjectsRequest{Ref: titleRef, Viewer: owner, Owners: []string{owner}, Collection: collection,
		Sort: "created", Ascending: true, Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(r.Objects))
	for _, o := range r.Objects {
		out = append(out, fromCore(o))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out, nil
}
