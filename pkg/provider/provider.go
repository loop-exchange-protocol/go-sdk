package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/loop-exchange-protocol/lxp/pkg/bundle"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type ApplyTarget struct {
	Workdir  string                    `json:"workdir"`
	Path     string                    `json:"path"`
	Children []protocol.ChildComponent `json:"children,omitempty"`
}

// Provider reconciles one globally identified content contract. Validate must
// not write Component content. Apply must be idempotent and retryable.
type Provider interface {
	Contract() spec.Contract
	Implementation() spec.Contract
	Match(ctx context.Context, path string) (int, error)
	Distributions() []string
	Validate(ctx context.Context, component spec.Component, store bundle.Store, target ApplyTarget) error
	Apply(ctx context.Context, component spec.Component, store bundle.Store, target ApplyTarget) (protocol.ResolvedRef, error)
	ExportComponent(ctx context.Context, ref protocol.ResolvedRef, mode string, store bundle.Store) (spec.Component, error)
}

type Adopter interface {
	Adopt(ctx context.Context, id, path, materialized string) (protocol.ResolvedRef, error)
}

type Tracker interface {
	Add(ctx context.Context, ref protocol.ResolvedRef, paths []string) error
	Status(ctx context.Context, ref protocol.ResolvedRef) ([]Change, error)
}

type NestedDiscoverer interface {
	DiscoverChildren(ctx context.Context, ref protocol.ResolvedRef) ([]string, error)
}

type BoundaryTracker interface {
	TrackChild(ctx context.Context, parent, child protocol.ResolvedRef) error
}

type Change struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type Registry struct {
	providers       map[string]Provider
	registrationErr error
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

func (r *Registry) Register(p Provider) {
	if p == nil {
		r.registrationErr = fmt.Errorf("cannot register a nil Provider")
		return
	}
	contract := p.Contract()
	implementation := p.Implementation()
	if !contract.Valid() || !implementation.Valid() {
		r.registrationErr = fmt.Errorf("invalid Provider registration %s -> %s", contract.String(), implementation.String())
		return
	}
	key := contract.String()
	if _, exists := r.providers[key]; exists {
		r.registrationErr = fmt.Errorf("duplicate Provider contract %s", key)
		return
	}
	r.providers[key] = p
}

func (r *Registry) Get(contract spec.Contract) (Provider, error) {
	if r.registrationErr != nil {
		return nil, r.registrationErr
	}
	p, ok := r.providers[contract.String()]
	if !ok {
		return nil, fmt.Errorf("unsupported provider contract %s", contract.String())
	}
	return p, nil
}

// Providers returns registered Providers in stable contract order without
// invoking Provider code. Callers can filter configuration bindings before
// running discovery hooks such as Match.
func (r *Registry) Providers() ([]Provider, error) {
	if r.registrationErr != nil {
		return nil, r.registrationErr
	}
	keys := make([]string, 0, len(r.providers))
	for key := range r.providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Provider, 0, len(keys))
	for _, key := range keys {
		out = append(out, r.providers[key])
	}
	return out, nil
}

func (r *Registry) Match(ctx context.Context, path string) (Provider, error) {
	p, _, err := r.MatchScore(ctx, path)
	if err == nil && p == nil {
		return nil, fmt.Errorf("no provider matches %q", path)
	}
	return p, err
}

func (r *Registry) MatchScore(ctx context.Context, path string) (Provider, int, error) {
	if r.registrationErr != nil {
		return nil, 0, r.registrationErr
	}
	var selected Provider
	best := 0
	matches := 0
	for _, p := range r.providers {
		score, err := p.Match(ctx, path)
		if err != nil {
			return nil, 0, fmt.Errorf("match provider %s: %w", p.Contract().String(), err)
		}
		if score > best {
			selected, best, matches = p, score, 1
		} else if score > 0 && score == best {
			matches++
		}
	}
	if matches > 1 {
		return nil, 0, fmt.Errorf("path %q matches multiple providers at priority %d", path, best)
	}
	return selected, best, nil
}
