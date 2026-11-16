package testenv

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Arr is a Radarr or Sonarr v3 API client.
type Arr struct {
	*Client
	Name string // radarr or sonarr
}

// M is a loosely typed JSON object, for API payloads the probes pass through or inspect.
type M = map[string]any

func (a *Arr) Get(ctx context.Context, path string, out any) error {
	return a.Do(ctx, "GET", "/api/v3"+path, nil, out)
}

func (a *Arr) Post(ctx context.Context, path string, body, out any) error {
	return a.Do(ctx, "POST", "/api/v3"+path, body, out)
}

func (a *Arr) Put(ctx context.Context, path string, body, out any) error {
	return a.Do(ctx, "PUT", "/api/v3"+path, body, out)
}

func (a *Arr) Delete(ctx context.Context, path string) error {
	return a.Do(ctx, "DELETE", "/api/v3"+path, nil, nil)
}

// Version returns the app version from /system/status.
func (a *Arr) Version(ctx context.Context) (string, error) {
	var st struct {
		Version string `json:"version"`
	}
	err := a.Get(ctx, "/system/status", &st)
	return st.Version, err
}

// Command is the state of a queued command.
type Command struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // queued, started, completed, failed, aborted, cancelled
	Result  string `json:"result"` // successful, unsuccessful, unknown
	Message string `json:"message"`
}

// RunCommand posts a command and waits until it is no longer queued or started.
func (a *Arr) RunCommand(ctx context.Context, body M, timeout time.Duration) (Command, error) {
	var cmd Command
	if err := a.Post(ctx, "/command", body, &cmd); err != nil {
		return cmd, err
	}
	err := WaitFor(ctx, timeout, 500*time.Millisecond, fmt.Sprintf("%s command %v", a.Name, body["name"]), func() (bool, error) {
		if err := a.Get(ctx, "/command/"+strconv.Itoa(cmd.ID), &cmd); err != nil {
			return false, err
		}
		return cmd.Status != "queued" && cmd.Status != "started", nil
	})
	return cmd, err
}

// FindByName returns the first item of a list endpoint whose "name" equals name.
func (a *Arr) FindByName(ctx context.Context, path, name string) (M, error) {
	var items []M
	if err := a.Get(ctx, path, &items); err != nil {
		return nil, err
	}
	for _, it := range items {
		if it["name"] == name {
			return it, nil
		}
	}
	return nil, nil
}

// ID returns m["id"] as an int.
func ID(m M) int {
	switch v := m["id"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
