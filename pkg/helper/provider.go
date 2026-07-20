package helper

import (
	"context"
	"fmt"

	"github.com/loop-exchange-protocol/lxp/pkg/bundle"
	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

// Provider is an out-of-process Provider bound by an exact Helper handshake.
type Provider struct {
	client         *client
	contract       spec.Contract
	implementation spec.Contract
	distributions  []string
	capabilities   map[string]bool
}

func NewProvider(ctx context.Context, root string, contract spec.Contract, implementation extension.Implementation, command []string) (*Provider, error) {
	c, err := startClient(ctx, command)
	if err != nil {
		return nil, err
	}
	initialized, err := initialize(ctx, c, root, extension.KindProvider, contract, implementation.Package)
	if err != nil {
		_ = c.close()
		return nil, err
	}
	if err := validateProviderHandshake(initialized); err != nil {
		_ = c.close()
		return nil, err
	}
	capabilities := make(map[string]bool, len(initialized.Capabilities))
	for _, capability := range initialized.Capabilities {
		capabilities[capability] = true
	}
	return &Provider{client: c, contract: contract, implementation: implementation.Package, distributions: initialized.Distributions, capabilities: capabilities}, nil
}

func validateProviderHandshake(initialized initializeResult) error {
	allowedCapabilities := map[string]bool{"adopt": true, "track": true, "discover-children": true, "track-child": true}
	seen := map[string]bool{}
	for _, capability := range initialized.Capabilities {
		if !allowedCapabilities[capability] || seen["capability:"+capability] {
			return fmt.Errorf("Helper advertised invalid or duplicate capability %q", capability)
		}
		seen["capability:"+capability] = true
	}
	allowedDistributions := map[string]bool{"reference": true, "embedded": true, "mirrored": true}
	for _, distribution := range initialized.Distributions {
		if !allowedDistributions[distribution] || seen["distribution:"+distribution] {
			return fmt.Errorf("Helper advertised invalid or duplicate distribution %q", distribution)
		}
		seen["distribution:"+distribution] = true
	}
	return nil
}

func (p *Provider) Contract() spec.Contract       { return p.contract }
func (p *Provider) Implementation() spec.Contract { return p.implementation }
func (p *Provider) Distributions() []string       { return append([]string(nil), p.distributions...) }
func (p *Provider) Close() error                  { return p.client.close() }

func (p *Provider) Match(ctx context.Context, path string) (int, error) {
	var result int
	err := p.client.call(ctx, "provider.match", struct {
		Path string `json:"path"`
	}{path}, &result)
	return result, err
}

type componentParams struct {
	Component spec.Component       `json:"component"`
	StoreRoot string               `json:"store_root"`
	Target    provider.ApplyTarget `json:"target"`
}

func (p *Provider) Validate(ctx context.Context, component spec.Component, store bundle.Store, target provider.ApplyTarget) error {
	return p.client.call(ctx, "provider.validate", componentParams{component, store.Root, target}, nil)
}

func (p *Provider) Apply(ctx context.Context, component spec.Component, store bundle.Store, target provider.ApplyTarget) (protocol.ResolvedRef, error) {
	var result protocol.ResolvedRef
	err := p.client.call(ctx, "provider.apply", componentParams{component, store.Root, target}, &result)
	return result, err
}

func (p *Provider) ExportComponent(ctx context.Context, ref protocol.ResolvedRef, mode string, store bundle.Store) (spec.Component, error) {
	var result spec.Component
	err := p.client.call(ctx, "provider.export", struct {
		Ref       protocol.ResolvedRef `json:"ref"`
		Mode      string               `json:"mode"`
		StoreRoot string               `json:"store_root"`
	}{ref, mode, store.Root}, &result)
	return result, err
}

func (p *Provider) Adopt(ctx context.Context, id, path, materialized string) (protocol.ResolvedRef, error) {
	if !p.capabilities["adopt"] {
		return protocol.ResolvedRef{ID: id, Path: path, Provider: p.contract, Source: "session", PoolPath: materialized, Materialized: materialized}, nil
	}
	var result protocol.ResolvedRef
	err := p.client.call(ctx, "provider.adopt", struct {
		ID           string `json:"id"`
		Path         string `json:"path"`
		Materialized string `json:"materialized"`
	}{id, path, materialized}, &result)
	return result, err
}

func (p *Provider) Add(ctx context.Context, ref protocol.ResolvedRef, paths []string) error {
	if !p.capabilities["track"] {
		return nil
	}
	return p.client.call(ctx, "provider.add", struct {
		Ref   protocol.ResolvedRef `json:"ref"`
		Paths []string             `json:"paths"`
	}{ref, paths}, nil)
}

func (p *Provider) Status(ctx context.Context, ref protocol.ResolvedRef) ([]provider.Change, error) {
	if !p.capabilities["track"] {
		return nil, nil
	}
	var result []provider.Change
	err := p.client.call(ctx, "provider.status", struct {
		Ref protocol.ResolvedRef `json:"ref"`
	}{ref}, &result)
	return result, err
}

func (p *Provider) DiscoverChildren(ctx context.Context, ref protocol.ResolvedRef) ([]string, error) {
	if !p.capabilities["discover-children"] {
		return nil, nil
	}
	var result []string
	err := p.client.call(ctx, "provider.discover-children", struct {
		Ref protocol.ResolvedRef `json:"ref"`
	}{ref}, &result)
	return result, err
}

func (p *Provider) TrackChild(ctx context.Context, parent, child protocol.ResolvedRef) error {
	if !p.capabilities["track-child"] {
		return nil
	}
	return p.client.call(ctx, "provider.track-child", struct {
		Parent protocol.ResolvedRef `json:"parent"`
		Child  protocol.ResolvedRef `json:"child"`
	}{parent, child}, nil)
}

var _ provider.Provider = (*Provider)(nil)
var _ provider.Adopter = (*Provider)(nil)
var _ provider.Tracker = (*Provider)(nil)
var _ provider.NestedDiscoverer = (*Provider)(nil)
var _ provider.BoundaryTracker = (*Provider)(nil)

func (p *Provider) String() string {
	return fmt.Sprintf("Helper Provider %s -> %s", p.contract.String(), p.implementation.String())
}
