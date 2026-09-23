// Package links is the business logic of the service: it creates short
// links, follows and deletes them, and purges the ones to a host.
package links

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/iaxel/tyr"
	"github.com/iaxel/tyr/examples/shortlink/store"
)

// Link is a short link.
type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// Created is a link just created, with the path of its resource, which
// REST sends as the Location of 201 Created.
type Created struct {
	Link
	Location string `json:"-" header:"Location"`
}

// CreateReq is a request to create a link. Without a code, the link gets a
// random one.
type CreateReq struct {
	URL  string `json:"url" validate:"required,http_url"`
	Code string `json:"code" validate:"omitempty,min=4,max=16"`
}

var codeRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate holds the rule tags can't express: the characters of the code.
func (r CreateReq) Validate() error {
	var v tyr.Violations
	if r.Code != "" && !codeRe.MatchString(r.Code) {
		v.Add("code", "only a-z, 0-9 and '-'")
	}
	return v.Err()
}

// GetReq is a request for a link.
type GetReq struct {
	Code string `json:"code" path:"code" validate:"required"`
}

// FollowRes is where a link leads: REST redirects to it, JSON-RPC returns
// it.
type FollowRes struct {
	URL string `json:"url" header:"Location"`
}

// DeleteReq is a request to delete a link.
type DeleteReq struct {
	Code string `json:"code" path:"code" validate:"required"`
}

// PurgeReq is a request to delete the links to a host, such as one that
// serves malware.
type PurgeReq struct {
	Host string `json:"host" validate:"required,max=253"`
}

// PurgeRes is the result of a purge.
type PurgeRes struct {
	Purged int `json:"purged"` // the number of links deleted
}

// Service implements the operations on links.
type Service struct {
	store   *store.Store
	newCode func() string
}

// New returns a service that keeps links in s.
func New(s *store.Store) *Service {
	return &Service{store: s, newCode: randomCode}
}

// Create creates a link.
func (s *Service) Create(ctx context.Context, req CreateReq) (Created, error) {
	l := store.Link{Code: req.Code, URL: req.URL, CreatedAt: time.Now().UTC()}
	var err error
	if l.Code != "" {
		err = s.store.Create(ctx, l)
	} else {
		// A random code is rarely taken; if it is, try another.
		for range 3 {
			l.Code = s.newCode()
			if err = s.store.Create(ctx, l); !errors.Is(err, store.ErrExists) {
				break
			}
		}
		if errors.Is(err, store.ErrExists) {
			// Not wrapped: the client didn't choose the code, so this is
			// no conflict of its making, but an internal error.
			err = fmt.Errorf("links: no free random code: %v", err)
		}
	}
	if err != nil {
		return Created{}, err
	}
	slog.InfoContext(ctx, "link created", "code", l.Code)
	return Created{Link: linkOf(l), Location: "/links/" + l.Code}, nil
}

// Get returns a link.
func (s *Service) Get(ctx context.Context, req GetReq) (Link, error) {
	l, err := s.store.Get(ctx, req.Code)
	if err != nil {
		return Link{}, err
	}
	return linkOf(l), nil
}

// Follow returns where a link leads.
func (s *Service) Follow(ctx context.Context, req GetReq) (FollowRes, error) {
	l, err := s.store.Get(ctx, req.Code)
	if err != nil {
		return FollowRes{}, err
	}
	return FollowRes{URL: l.URL}, nil
}

// Delete deletes a link.
func (s *Service) Delete(ctx context.Context, req DeleteReq) (struct{}, error) {
	if err := s.store.Delete(ctx, req.Code); err != nil {
		return struct{}{}, err
	}
	slog.InfoContext(ctx, "link deleted", "code", req.Code)
	return struct{}{}, nil
}

// Purge deletes the links to a host.
func (s *Service) Purge(ctx context.Context, req PurgeReq) (PurgeRes, error) {
	n := s.store.DeleteFunc(ctx, func(l store.Link) bool {
		u, err := url.Parse(l.URL)
		return err == nil && strings.EqualFold(u.Hostname(), req.Host)
	})
	slog.InfoContext(ctx, "links purged", "host", req.Host, "purged", n)
	return PurgeRes{Purged: n}, nil
}

// randomCode returns a random code of 7 characters of a-z and 2-7.
func randomCode() string {
	return strings.ToLower(rand.Text()[:7])
}

// linkOf returns the stored link l as a Link.
func linkOf(l store.Link) Link {
	return Link{Code: l.Code, URL: l.URL, CreatedAt: l.CreatedAt}
}
