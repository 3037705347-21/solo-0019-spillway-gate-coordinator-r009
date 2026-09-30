package coordination_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/spillway-gate-coordinator/internal/bootstrap"
)

type grantClient struct {
	t      *testing.T
	server *httptest.Server
}

func newGrantClient(t *testing.T) *grantClient {
	t.Helper()
	return &grantClient{t: t, server: httptest.NewServer(bootstrap.NewHandler())}
}

func (c *grantClient) close() {
	c.server.Close()
}

func (c *grantClient) get(path string) (int, map[string]any) {
	c.t.Helper()
	response, err := http.Get(c.server.URL + path)
	if err != nil {
		c.t.Fatalf("get %s: %v", path, err)
	}
	defer response.Body.Close()

	decoded := map[string]any{}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		c.t.Fatalf("decode %s response: %v", path, err)
	}
	return response.StatusCode, decoded
}

func (c *grantClient) post(path string, payload any) (int, map[string]any) {
	c.t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatalf("marshal payload: %v", err)
	}
	response, err := http.Post(c.server.URL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		c.t.Fatalf("post %s: %v", path, err)
	}
	defer response.Body.Close()

	decoded := map[string]any{}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		c.t.Fatalf("decode %s response: %v", path, err)
	}
	return response.StatusCode, decoded
}

func (c *grantClient) object(body map[string]any, key string) map[string]any {
	c.t.Helper()
	value, ok := body[key].(map[string]any)
	if !ok {
		c.t.Fatalf("response missing object %q: %v", key, body)
	}
	return value
}

func (c *grantClient) str(object map[string]any, key string) string {
	c.t.Helper()
	value, ok := object[key].(string)
	if !ok {
		c.t.Fatalf("object missing string %q: %v", key, object)
	}
	return value
}

func (c *grantClient) registerGate(id string) {
	c.t.Helper()
	status, body := c.post("/gates", map[string]any{
		"id":               id,
		"name":             id,
		"max_aperture":     4.0,
		"initial_aperture": 0.5,
	})
	if status != http.StatusCreated {
		c.t.Fatalf("register gate %s: expected 201, got %d %v", id, status, body)
	}
}

func (c *grantClient) requestCommand(gateID string, target float64) string {
	c.t.Helper()
	status, body := c.post("/commands", map[string]any{
		"gate_id":         gateID,
		"target_aperture": target,
		"reason":          "调整开度",
		"requested_by":    "dispatcher-lin",
	})
	if status != http.StatusCreated {
		c.t.Fatalf("request command on %s: expected 201, got %d %v", gateID, status, body)
	}
	return c.str(c.object(body, "command"), "id")
}

func (c *grantClient) clearCommand(commandID string) string {
	c.t.Helper()
	status, body := c.post("/commands/"+commandID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": "safety-wu",
		"note":     "联锁条件满足",
	})
	if status != http.StatusOK {
		c.t.Fatalf("clear command %s: expected 200, got %d %v", commandID, status, body)
	}
	return c.str(c.object(body, "command"), "execution_token")
}

func (c *grantClient) execute(commandID, token string) (int, map[string]any) {
	c.t.Helper()
	return c.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": false,
	})
}

// TestExecutionGrantBoundToCommandAndGate covers the cross-use attack:
// an execution credential issued for gate A's command must not move gate B's
// already-approved command, the failed attempt must leave gate A's credential
// valid, and executing gate A afterwards must not consume gate B's credential.
func TestExecutionGrantBoundToCommandAndGate(t *testing.T) {
	client := newGrantClient(t)
	defer client.close()

	client.registerGate("gate-a")
	client.registerGate("gate-b")
	commandA := client.requestCommand("gate-a", 2.0)
	commandB := client.requestCommand("gate-b", 3.0)
	tokenA := client.clearCommand(commandA)
	tokenB := client.clearCommand(commandB)

	// Attack: submit gate A's credential against gate B's approved command.
	status, body := client.execute(commandB, tokenA)
	if status != http.StatusConflict {
		t.Fatalf("cross-gate token use: expected 409 conflict, got %d %v", status, body)
	}

	// The rejected attempt must not have changed gate B's state.
	_, gateBody := client.get("/gates/gate-b")
	gateB := client.object(gateBody, "gate")
	if gateStatus := client.str(gateB, "status"); gateStatus != "reserved" {
		t.Fatalf("gate B status after rejected cross-use: expected reserved, got %q", gateStatus)
	}

	// Gate A's credential must still be valid after the failed attack.
	status, body = client.execute(commandA, tokenA)
	if status != http.StatusOK {
		t.Fatalf("gate A credential after rejected cross-use: expected 200, got %d %v", status, body)
	}
	if commandStatus := client.str(client.object(body, "command"), "status"); commandStatus != "completed" {
		t.Fatalf("gate A command status: expected completed, got %q", commandStatus)
	}

	// Executing gate A must not have consumed gate B's credential.
	status, body = client.execute(commandB, tokenB)
	if status != http.StatusOK {
		t.Fatalf("gate B credential after gate A executed: expected 200, got %d %v", status, body)
	}
	if commandStatus := client.str(client.object(body, "command"), "status"); commandStatus != "completed" {
		t.Fatalf("gate B command status: expected completed, got %q", commandStatus)
	}

	// A credential cannot be reused after its command reached a terminal state.
	status, _ = client.execute(commandA, tokenA)
	if status != http.StatusConflict {
		t.Fatalf("reuse consumed token: expected 409 conflict, got %d", status)
	}
}

// TestSimulatedFaultDoesNotConsumeOtherGrant verifies that a faulted
// execution on one gate leaves the other gate's valid credential untouched.
func TestSimulatedFaultDoesNotConsumeOtherGrant(t *testing.T) {
	client := newGrantClient(t)
	defer client.close()

	client.registerGate("gate-a")
	client.registerGate("gate-b")
	commandA := client.requestCommand("gate-a", 2.0)
	commandB := client.requestCommand("gate-b", 3.0)
	tokenA := client.clearCommand(commandA)
	tokenB := client.clearCommand(commandB)

	status, body := client.post("/commands/"+commandA+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          tokenA,
		"simulate_fault": true,
	})
	if status != http.StatusOK {
		t.Fatalf("faulted execution: expected 200, got %d %v", status, body)
	}
	if commandStatus := client.str(client.object(body, "command"), "status"); commandStatus != "aborted" {
		t.Fatalf("faulted command status: expected aborted, got %q", commandStatus)
	}

	// Gate B's credential must remain valid and executable.
	status, body = client.execute(commandB, tokenB)
	if status != http.StatusOK {
		t.Fatalf("gate B credential after gate A faulted: expected 200, got %d %v", status, body)
	}
	if commandStatus := client.str(client.object(body, "command"), "status"); commandStatus != "completed" {
		t.Fatalf("gate B command status: expected completed, got %q", commandStatus)
	}
}

// TestFailedExecutionDoesNotConsumeGrant verifies that a rejected attempt
// (unknown token) leaves every valid credential untouched.
func TestFailedExecutionDoesNotConsumeGrant(t *testing.T) {
	client := newGrantClient(t)
	defer client.close()

	client.registerGate("gate-a")
	commandA := client.requestCommand("gate-a", 2.0)
	tokenA := client.clearCommand(commandA)

	status, body := client.execute(commandA, "not-a-valid-token")
	if status != http.StatusConflict {
		t.Fatalf("bogus token: expected 409, got %d %v", status, body)
	}

	status, body = client.execute(commandA, tokenA)
	if status != http.StatusOK {
		t.Fatalf("valid credential after failed attempt: expected 200, got %d %v", status, body)
	}
	if commandStatus := client.str(client.object(body, "command"), "status"); commandStatus != "completed" {
		t.Fatalf("command status: expected completed, got %q", commandStatus)
	}
}
