// Package links is the business logic of the service: it creates short
// links, resolves them and purges the ones to a host.
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

// CreateReq is a request to create a link. Without a code, the link gets a
// random one.
type CreateReq struct {
	URL  string `json:"url" validate:"required,url"`
	Code string `json:"code" validate:"omitempty,min=4,max=16"`
}

var codeRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate holds the rules tags can't express: the scheme of the URL and
// the characters of the code. It runs after the tags passed, so the URL
// parses.
func (r CreateReq) Validate() error {
	var v tyr.Violations
	// The url rule takes any scheme, javascript: and mailto: too.
	if u, err := url.Parse(r.URL); err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		v.Add("url", "must be an http or https URL")
	}
	if r.Code != "" && !codeRe.MatchString(r.Code) {
		v.Add("code", "only a-z, 0-9 and '-'")
	}
	return v.Err()
}

// GetReq is a request for a link.
type GetReq struct {
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
func (s *Service) Create(ctx context.Context, req CreateReq) (Link, error) {
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
		return Link{}, err
	}
	slog.InfoContext(ctx, "link created", "code", l.Code)
	return linkOf(l), nil
}

// Get returns a link.
func (s *Service) Get(ctx context.Context, req GetReq) (Link, error) {
	l, err := s.store.Get(ctx, req.Code)
	if err != nil {
		return Link{}, err
	}
	return linkOf(l), nil
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
