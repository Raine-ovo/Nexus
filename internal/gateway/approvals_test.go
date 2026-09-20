package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/approval"
)

func TestGatewayApprovalEndpoints(t *testing.T) {
	am := approval.NewManager(time.Minute)
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, stubSupervisor{output: "ok"}, nil)
	g.SetApprovalManager(am)
	mux := g.newPrimaryMux()

	r := am.Create("s1", "write_file", map[string]interface{}{"path": "x"}, "needs approval")

	// List pending.
	req := httptest.NewRequest(http.MethodGet, "/api/approvals", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), r.ID) {
		t.Fatalf("list missing approval id, body=%s", rec.Body.String())
	}

	// Approve one-time.
	req = httptest.NewRequest(http.MethodPost, "/api/approvals/"+r.ID+"/approve", strings.NewReader(`{"persist":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Get and verify status.
	req = httptest.NewRequest(http.MethodGet, "/api/approvals/"+r.ID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got approval.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != approval.StatusApproved {
		t.Fatalf("status = %s, want approved", got.Status)
	}
}

func TestGatewayApprovalEndpointsDisabled(t *testing.T) {
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, stubSupervisor{output: "ok"}, nil)
	mux := g.newPrimaryMux()

	req := httptest.NewRequest(http.MethodGet, "/api/approvals", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 when disabled, got %d", rec.Code)
	}
}
