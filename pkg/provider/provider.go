package provider

import (
	"context"
	"fmt"

	"github.com/loop-exchange-protocol/go-sdk/pkg/bundle"
	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type MaterializeTarget struct {
	Workdir string
	Path    string
}

type Provider interface {
	Name() string
	Contract() string
	Match(ctx context.Context, path string) (int, error)
	Plan(ctx context.Context, ref protocol.ResolvedRef) (Plan, error)
	Distributions() []string
	Resolve(ctx context.Context, ref protocol.RefSpec) (protocol.ResolvedRef, error)
	Materialize(ctx context.Context, ref protocol.ResolvedRef, target MaterializeTarget) (protocol.ResolvedRef, error)
	ExportComponent(ctx context.Context, ref protocol.ResolvedRef, mode string, store bundle.Store) (spec.Component, error)
	Restore(ctx context.Context, component spec.Component, store bundle.Store, target MaterializeTarget) (protocol.ResolvedRef, error)
	Activate(ctx context.Context, component protocol.ResolvedRef) error
}

type Plan struct {
	Actions      []string `json:"actions"`
	Requirements []string `json:"requirements,omitempty"`
}

type Adopter interface {
	Adopt(ctx context.Context, id, path, materialized string) (protocol.ResolvedRef, error)
}

// Tracker is implemented by providers that own native change-selection
// semantics. Paths are relative to the component root.
type Tracker interface {
	Add(ctx context.Context, ref protocol.ResolvedRef, paths []string) error
	Status(ctx context.Context, ref protocol.ResolvedRef) ([]Change, error)
}

type Change struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
	return p, nil
}

func (r *Registry) Match(ctx context.Context, path string) (Provider, error) {
	p, _, err := r.MatchScore(ctx, path)
	if err == nil && p == nil {
		return nil, fmt.Errorf("no provider matches %q", path)
	}
	return p, err
}

func (r *Registry) MatchScore(ctx context.Context, path string) (Provider, int, error) {
	var selected Provider
	best := 0
	matches := 0
	for _, p := range r.providers {
		score, err := p.Match(ctx, path)
		if err != nil {
			return nil, 0, fmt.Errorf("match provider %q: %w", p.Name(), err)
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
