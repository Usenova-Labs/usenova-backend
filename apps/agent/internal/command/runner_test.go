package command

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/controlplane"
	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/runtime"
)

// fakeSource is a control plane that hands out canned commands.
type fakeSource struct {
	commands  []*controlplane.Command
	fetchErr  error
	reportErr error
	results   []controlplane.CommandResult
}

func (f *fakeSource) FetchCommand(ctx context.Context) (*controlplane.Command, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if len(f.commands) == 0 {
		return nil, nil
	}
	command := f.commands[0]
	f.commands = f.commands[1:]
	return command, nil
}

func (f *fakeSource) ReportCommandResult(ctx context.Context, result controlplane.CommandResult) error {
	if f.reportErr != nil {
		return f.reportErr
	}
	f.results = append(f.results, result)
	return nil
}

// fakeServices records the calls the runner makes.
type fakeServices struct {
	deployed  []docker.ServiceSpec
	stopped   []string
	removed   []string
	deployErr error
	stopErr   error
	removeErr error
}

func (f *fakeServices) DeployService(ctx context.Context, spec docker.ServiceSpec) (*runtime.Service, error) {
	if f.deployErr != nil {
		return nil, f.deployErr
	}
	f.deployed = append(f.deployed, spec)
	return &runtime.Service{Spec: spec}, nil
}

func (f *fakeServices) StopService(ctx context.Context, name string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, name)
	return nil
}

func (f *fakeServices) RemoveService(ctx context.Context, name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}

func testRunner(source CommandSource, services ServiceManager) *Runner {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return NewRunner(source, services, time.Second, log)
}

func TestRunner_PollDeploysService(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:      "cmd-1",
		Type:    controlplane.CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx:latest"}`),
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(services.deployed) != 1 {
		t.Fatalf("expected 1 deploy, got %d", len(services.deployed))
	}
	if services.deployed[0].Name != "api" || services.deployed[0].Image != "nginx:latest" {
		t.Errorf("unexpected service spec: %+v", services.deployed[0])
	}

	if len(source.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(source.results))
	}
	result := source.results[0]
	if result.Status != controlplane.CommandStatusSucceeded {
		t.Errorf("expected status %q, got %q", controlplane.CommandStatusSucceeded, result.Status)
	}
	if result.CommandID != "cmd-1" {
		t.Errorf("expected command ID %q, got %q", "cmd-1", result.CommandID)
	}
}

func TestRunner_PollStopsAndRemovesService(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:      "cmd-1",
		Type:    controlplane.CommandTypeStop,
		Payload: json.RawMessage(`{"name":"api"}`),
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(services.stopped) != 1 || services.stopped[0] != "api" {
		t.Errorf("expected the service to be stopped, got %v", services.stopped)
	}

	source.commands = []*controlplane.Command{{
		ID:      "cmd-2",
		Type:    controlplane.CommandTypeRemove,
		Payload: json.RawMessage(`{"name":"api"}`),
	}}

	testRunner(source, services).Poll(context.Background())

	if len(services.removed) != 1 || services.removed[0] != "api" {
		t.Errorf("expected the service to be removed, got %v", services.removed)
	}
}

func TestRunner_PollNoCommand(t *testing.T) {
	source := &fakeSource{}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(source.results) != 0 {
		t.Errorf("expected no result to be reported, got %d", len(source.results))
	}
	if len(services.deployed) != 0 {
		t.Errorf("expected no service to be touched, got %d", len(services.deployed))
	}
}

func TestRunner_PollFetchError(t *testing.T) {
	source := &fakeSource{fetchErr: errors.New("boom")}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(source.results) != 0 {
		t.Errorf("expected no result to be reported, got %d", len(source.results))
	}
}

func TestRunner_PollUnsupportedCommand(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:   "cmd-1",
		Type: "restart",
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(source.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(source.results))
	}
	if source.results[0].Status != controlplane.CommandStatusFailed {
		t.Errorf("expected status %q, got %q", controlplane.CommandStatusFailed, source.results[0].Status)
	}
	if source.results[0].Error == "" {
		t.Error("expected an error message")
	}
}

func TestRunner_PollInvalidPayload(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:      "cmd-1",
		Type:    controlplane.CommandTypeDeploy,
		Payload: json.RawMessage(`not json`),
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(services.deployed) != 0 {
		t.Errorf("expected no service to be deployed, got %d", len(services.deployed))
	}
	if len(source.results) != 1 || source.results[0].Status != controlplane.CommandStatusFailed {
		t.Fatalf("expected a failed result, got %+v", source.results)
	}
}

func TestRunner_PollMissingServiceName(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:      "cmd-1",
		Type:    controlplane.CommandTypeStop,
		Payload: json.RawMessage(`{}`),
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(services.stopped) != 0 {
		t.Errorf("expected no service to be stopped, got %v", services.stopped)
	}
	if len(source.results) != 1 || source.results[0].Status != controlplane.CommandStatusFailed {
		t.Fatalf("expected a failed result, got %+v", source.results)
	}
}

func TestRunner_PollExpiredCommand(t *testing.T) {
	expired := time.Now().Add(-time.Minute)
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:        "cmd-1",
		Type:      controlplane.CommandTypeDeploy,
		Payload:   json.RawMessage(`{"name":"api","image":"nginx"}`),
		ExpiresAt: &expired,
	}}}
	services := &fakeServices{}

	testRunner(source, services).Poll(context.Background())

	if len(services.deployed) != 0 {
		t.Errorf("expected an expired command not to be executed, got %d deploys", len(services.deployed))
	}
	if len(source.results) != 1 || source.results[0].Status != controlplane.CommandStatusFailed {
		t.Fatalf("expected a failed result, got %+v", source.results)
	}
}

func TestRunner_PollDeployError(t *testing.T) {
	source := &fakeSource{commands: []*controlplane.Command{{
		ID:      "cmd-1",
		Type:    controlplane.CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	}}}
	services := &fakeServices{deployErr: errors.New("docker is down")}

	testRunner(source, services).Poll(context.Background())

	if len(source.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(source.results))
	}
	if source.results[0].Status != controlplane.CommandStatusFailed {
		t.Errorf("expected status %q, got %q", controlplane.CommandStatusFailed, source.results[0].Status)
	}
	if source.results[0].Error != "docker is down" {
		t.Errorf("expected the deploy error to be reported, got %q", source.results[0].Error)
	}
}

func TestRunner_RunStopsOnContextCancel(t *testing.T) {
	source := &fakeSource{}
	services := &fakeServices{}
	runner := testRunner(source, services)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		runner.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the command loop did not stop when the context was cancelled")
	}
}
