package helper

import (
	"context"
	"fmt"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

// Checker is an out-of-process requirement Checker.
type Checker struct {
	client         *client
	contract       spec.Contract
	implementation spec.Contract
}

func NewChecker(ctx context.Context, root string, contract spec.Contract, implementation extension.Implementation, command []string) (*Checker, error) {
	c, err := startClient(ctx, command)
	if err != nil {
		return nil, err
	}
	initialized, err := initialize(ctx, c, root, extension.KindChecker, contract, implementation.Package)
	if err != nil {
		_ = c.close()
		return nil, err
	}
	if len(initialized.Distributions) != 0 || len(initialized.Capabilities) != 0 {
		_ = c.close()
		return nil, fmt.Errorf("Checker Helper advertised Provider-only fields")
	}
	return &Checker{client: c, contract: contract, implementation: implementation.Package}, nil
}

func (c *Checker) Contract() spec.Contract       { return c.contract }
func (c *Checker) Implementation() spec.Contract { return c.implementation }
func (c *Checker) Close() error                  { return c.client.close() }

func (c *Checker) Check(ctx context.Context, requirement spec.Requirement, opts runtime.Options) (runtime.Observation, error) {
	var result runtime.Observation
	err := c.client.call(ctx, "checker.check", struct {
		Requirement spec.Requirement `json:"requirement"`
		Options     runtime.Options  `json:"options"`
	}{requirement, opts}, &result)
	return result, err
}

var _ runtime.Checker = (*Checker)(nil)
