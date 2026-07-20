package helper

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

var testCheckerContract = spec.Contract{Namespace: "example.test", Name: "policy", Version: "v1"}
var testCheckerImplementation = spec.Contract{Namespace: "example.test", Name: "checker-policy", Version: "1.0.0"}

type testChecker struct{}

func (testChecker) Contract() spec.Contract       { return testCheckerContract }
func (testChecker) Implementation() spec.Contract { return testCheckerImplementation }
func (testChecker) Check(ctx context.Context, requirement spec.Requirement, _ runtime.Options) (runtime.Observation, error) {
	if requirement.ID == "block" {
		<-ctx.Done()
		return runtime.Observation{}, fmt.Errorf("blocking Checker: %w", ctx.Err())
	}
	return runtime.Observation{ID: requirement.ID, Checker: testCheckerContract, Status: "ready", Implementation: "test-helper"}, nil
}

func TestCheckerHelperCancellationIsBounded(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	checker, err := NewChecker(context.Background(), t.TempDir(), testCheckerContract, extension.Implementation{
		Source: "helper", Package: testCheckerImplementation,
	}, []string{executable, "-test.run=^TestCheckerHelperServerProcess$", "--", "checker-helper-server"})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = checker.Check(ctx, spec.Requirement{ID: "block", Check: spec.Check{Checker: testCheckerContract}}, runtime.Options{})
	if err == nil {
		t.Fatal("blocking Helper unexpectedly succeeded")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("Helper cancellation was not bounded: %v", time.Since(started))
	}
}

func TestCheckerHelperServerProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "checker-helper-server" {
		t.Skip("Helper subprocess entrypoint")
	}
	if err := ServeChecker(func(string) runtime.Checker { return testChecker{} }); err != nil {
		t.Fatal(err)
	}
}

func TestCheckerHelperRoundTrip(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	checker, err := NewChecker(context.Background(), t.TempDir(), testCheckerContract, extension.Implementation{
		Source: "helper", Package: testCheckerImplementation,
	}, []string{executable, "-test.run=^TestCheckerHelperServerProcess$", "--", "checker-helper-server"})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	observation, err := checker.Check(context.Background(), spec.Requirement{
		ID: "policy", Check: spec.Check{Checker: testCheckerContract},
	}, runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != "ready" || observation.Implementation != "test-helper" {
		t.Fatalf("unexpected observation %#v", observation)
	}
}
