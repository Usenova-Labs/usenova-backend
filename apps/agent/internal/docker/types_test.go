package docker

import (
	"encoding/json"
	"testing"
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
