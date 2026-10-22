package docker

import (
	"encoding/json"
	"testing"
	"time"
)

// TestServiceSpecJSONDecodesClientKeys locks the agent API contract: clients
// (CLI, control plane) send snake_case keys and the spec must decode them.
func TestServiceSpecJSONDecodesClientKeys(t *testing.T) {
	payload := `{
		"name": "web",
		"image": "nginx:1.25",
		"restart_policy": "always",
		"ports": {"8080": "80"},
		"env": ["A=1", "ORIGINS=a,b"],
		"health_check": {"path": "/health", "port": 8080, "type": "http"}
	}`

	var spec ServiceSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if spec.Name != "web" {
		t.Errorf("Name = %q, want web", spec.Name)
	}
	if spec.Image != "nginx:1.25" {
		t.Errorf("Image = %q, want nginx:1.25", spec.Image)
	}
	if spec.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %q, want always (restart_policy key must decode)", spec.RestartPolicy)
	}
	if spec.Ports["8080"] != "80" {
		t.Errorf("Ports = %v, want 8080:80", spec.Ports)
	}
	if len(spec.Env) != 2 || spec.Env[1] != "ORIGINS=a,b" {
		t.Errorf("Env = %v, want [A=1 ORIGINS=a,b]", spec.Env)
	}
	if spec.HealthCheck == nil {
		t.Fatal("HealthCheck = nil, want health_check key decoded")
	}
	if spec.HealthCheck.Path != "/health" || spec.HealthCheck.Port != 8080 || spec.HealthCheck.Type != "http" {
		t.Errorf("HealthCheck = %+v, want {http /health 8080}", spec.HealthCheck)
	}
}

// TestServiceSpecJSONMarshalUsesSnakeCase verifies responses and persisted
// label meta expose the same snake_case keys clients expect.
func TestServiceSpecJSONMarshalUsesSnakeCase(t *testing.T) {
	spec := ServiceSpec{
		Name:          "web",
		Image:         "nginx",
		RestartPolicy: "unless-stopped",
		HealthCheck:   &HealthConfig{Type: "tcp", Port: 5432},
		Resources:     &ResourceLimits{CPULimit: 1e9, MemoryLimit: 512 << 20},
	}

	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	for _, key := range []string{"name", "image", "restart_policy", "health_check", "resources"} {
		if _, ok := m[key]; !ok {
			t.Errorf("marshaled spec missing key %q: %s", key, b)
		}
	}
	for _, key := range []string{"Name", "RestartPolicy", "HealthCheck"} {
		if _, ok := m[key]; ok {
			t.Errorf("marshaled spec still uses default key %q: %s", key, b)
		}
	}

	hc, _ := m["health_check"].(map[string]any)
	if hc == nil || hc["type"] != "tcp" || hc["port"].(float64) != 5432 {
		t.Errorf("health_check = %v, want type=tcp port=5432", hc)
	}
}

// TestHealthConfigDecodesLegacyMeta keeps previously persisted label meta
// readable after tags were introduced (Go matches field names
// case-insensitively when the tag differs).
func TestHealthConfigDecodesLegacyMeta(t *testing.T) {
	legacy := `{"health_check":{"Type":"http","Path":"/h","Port":8080,"Retries":5}}`

	var meta struct {
		HealthCheck *HealthConfig `json:"health_check,omitempty"`
	}
	if err := json.Unmarshal([]byte(legacy), &meta); err != nil {
		t.Fatalf("unmarshal legacy meta: %v", err)
	}
	if meta.HealthCheck == nil {
		t.Fatal("HealthCheck = nil, want legacy meta decoded")
	}
	if meta.HealthCheck.Type != "http" || meta.HealthCheck.Path != "/h" ||
		meta.HealthCheck.Port != 8080 || meta.HealthCheck.Retries != 5 {
		t.Errorf("HealthCheck = %+v, want legacy values preserved", meta.HealthCheck)
	}
}

// TestServiceSpecJSONDecodesLegacyRequestShape covers the top-level request
// keys clients sent before JSON tags existed. Keys whose folded names do not
// equal the snake_case tags (HealthCheck, RestartPolicy, DependsOn, and the
// nested StartPeriod / CPULimit-style fields) would otherwise be silently
// dropped by encoding/json.
func TestServiceSpecJSONDecodesLegacyRequestShape(t *testing.T) {
	legacy := `{
		"Name": "web",
		"Image": "nginx:1.25",
		"RestartPolicy": "unless-stopped",
		"Ports": {"8080": "80"},
		"Env": ["A=1"],
		"Volumes": ["/data:/data"],
		"Labels": {"team": "core"},
		"DependsOn": ["db"],
		"HealthCheck": {"Type": "http", "Path": "/health", "Port": 8080, "StartPeriod": 45000000000, "Retries": 4},
		"Resources": {"CPULimit": 1000000000, "MemoryLimit": 536870912, "CPUReserve": 500000000, "MemoryReserve": 268435456}
	}`

	var spec ServiceSpec
	if err := json.Unmarshal([]byte(legacy), &spec); err != nil {
		t.Fatalf("unmarshal legacy spec: %v", err)
	}

	if spec.Name != "web" || spec.Image != "nginx:1.25" {
		t.Errorf("Name/Image = %q/%q, want web/nginx:1.25", spec.Name, spec.Image)
	}
	if spec.RestartPolicy != "unless-stopped" {
		t.Errorf("RestartPolicy = %q, want legacy RestartPolicy decoded", spec.RestartPolicy)
	}
	if len(spec.DependsOn) != 1 || spec.DependsOn[0] != "db" {
		t.Errorf("DependsOn = %v, want legacy DependsOn decoded", spec.DependsOn)
	}
	if spec.Ports["8080"] != "80" || len(spec.Env) != 1 || spec.Env[0] != "A=1" ||
		len(spec.Volumes) != 1 || spec.Labels["team"] != "core" {
		t.Errorf("single-word legacy keys lost: %+v", spec)
	}
	if spec.HealthCheck == nil {
		t.Fatal("HealthCheck = nil, want legacy HealthCheck decoded")
	}
	if spec.HealthCheck.Type != "http" || spec.HealthCheck.Path != "/health" ||
		spec.HealthCheck.Port != 8080 || spec.HealthCheck.Retries != 4 {
		t.Errorf("HealthCheck = %+v, want legacy values", *spec.HealthCheck)
	}
	if spec.HealthCheck.StartPeriod != 45*time.Second {
		t.Errorf("StartPeriod = %s, want legacy StartPeriod decoded", spec.HealthCheck.StartPeriod)
	}
	if spec.Resources == nil {
		t.Fatal("Resources = nil, want legacy Resources decoded")
	}
	if spec.Resources.CPULimit != 1e9 || spec.Resources.MemoryLimit != 512<<20 ||
		spec.Resources.CPUReserve != 5e8 || spec.Resources.MemoryReserve != 256<<20 {
		t.Errorf("Resources = %+v, want legacy CPULimit/MemoryLimit/CPUReserve/MemoryReserve", *spec.Resources)
	}
}

// TestServiceSpecJSONPrefersCurrentShape: when both shapes appear, the
// snake_case keys win so a transition-period client is never downgraded.
func TestServiceSpecJSONPrefersCurrentShape(t *testing.T) {
	payload := `{
		"restart_policy": "always",
		"RestartPolicy": "no",
		"health_check": {"path": "/new"},
		"HealthCheck": {"path": "/old"}
	}`

	var spec ServiceSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %q, want always (current shape wins)", spec.RestartPolicy)
	}
	if spec.HealthCheck == nil || spec.HealthCheck.Path != "/new" {
		t.Errorf("HealthCheck = %+v, want path=/new (current shape wins)", spec.HealthCheck)
	}
}

// TestServiceSpecJSONDecodesRestartLimits locks the agent API contract for
// the per-service restart limits.
func TestServiceSpecJSONDecodesRestartLimits(t *testing.T) {
	payload := `{
		"name": "web",
		"image": "nginx:1.25",
		"max_restarts": 5,
		"restart_window_seconds": 300,
		"restart_backoff_seconds": 15
	}`

	var spec ServiceSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if spec.MaxRestarts != 5 {
		t.Errorf("MaxRestarts = %d, want 5", spec.MaxRestarts)
	}
	if spec.RestartWindowSeconds != 300 {
		t.Errorf("RestartWindowSeconds = %d, want 300", spec.RestartWindowSeconds)
	}
	if spec.RestartBackoffSeconds != 15 {
		t.Errorf("RestartBackoffSeconds = %d, want 15", spec.RestartBackoffSeconds)
	}

	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"max_restarts", "restart_window_seconds", "restart_backoff_seconds"} {
		if _, ok := m[key]; !ok {
			t.Errorf("marshaled spec missing key %q: %s", key, b)
		}
	}
}
