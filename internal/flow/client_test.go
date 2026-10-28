package flow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunsPaginatesFetchesLinksAndRetries(t *testing.T) {
	var definitionHits atomic.Int32
	var page2Hits atomic.Int32
	var slept atomic.Int32

	var srv *httptest.Server
	var blob *httptest.Server
	blob = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("blob request included flow authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"from":"blob"}`)
	}))
	t.Cleanup(blob.Close)

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("path %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch {
		case strings.Contains(r.URL.Path, "/actions"):
			_, _ = io.WriteString(w, `{
			  "value": [
			    {
			      "name": "Compose",
			      "properties": {
			        "status": "Succeeded",
			        "startTime": "2026-10-27T23:56:35Z",
			        "endTime": "2026-10-27T23:56:35Z",
			        "inputsLink": {"uri": "`+blob.URL+`/compose"},
			        "outputs": 3134
			      }
			    },
			    {
			      "name": "Secured",
			      "properties": {
			        "status": "Skipped",
			        "startTime": "2026-10-27T23:56:35Z",
			        "endTime": "2026-10-27T23:56:35Z",
			        "outputsLink": {"uri": "`+srv.URL+`/secured"}
			      }
			    }
			  ]
			}`)
		case strings.HasSuffix(r.URL.Path, "/secured"):
			http.Error(w, `{"message":"secured"}`, http.StatusForbidden)
		case strings.HasSuffix(r.URL.Path, "/runs/run2"):
			_, _ = io.WriteString(w, `{
			  "name": "run2",
			  "properties": {
			    "startTime": "2026-10-27T22:00:00Z",
			    "endTime": "",
			    "status": "Failed",
			    "trigger": {"status": "Succeeded", "inputs": {"kind": "run"}}
			  }
			}`)
		case strings.HasSuffix(r.URL.Path, "/runs"):
			if r.URL.Query().Get("page") == "2" {
				page2Hits.Add(1)
				_, _ = io.WriteString(w, `{"value":[{"name":"run2","properties":{"status":"Failed","startTime":"2026-10-27T22:00:00Z"}}]}`)
				return
			}
			if r.URL.Query().Get("$filter") != "status eq 'Failed' and startTime ge 2026-10-01T00:00:00Z" {
				t.Errorf("filter %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{
			  "value": [{
			    "name": "run1",
			    "properties": {
			      "startTime": "2026-10-27T23:56:32Z",
			      "endTime": "2026-10-27T23:58:29Z",
			      "status": "Failed",
			      "error": {"code":"ActionFailed","message":"An action failed."},
			      "trigger": {
			        "status": "Succeeded",
			        "startTime": "2026-10-27T23:56:31Z",
			        "endTime": "2026-10-27T23:56:31Z",
			        "inputs": {"host":"sharepoint"}
			      }
			    }
			  }],
			  "nextLink": "`+srv.URL+`/providers/Microsoft.ProcessSimple/environments/env/flows/flow/runs?page=2"
			}`)
		default:
			if definitionHits.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}
			_, _ = io.WriteString(w, `{
			  "properties": {
			    "definition": {
			      "triggers": {"When": {}},
			      "actions": {"Compose": {}, "Secured": {}}
			    }
			  }
			}`)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient("tok", false)
	client.baseURL = srv.URL
	client.sleep = func(d time.Duration) {
		if d != time.Second {
			t.Errorf("sleep %s", d)
		}
		slept.Add(1)
	}
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	names, err := client.FetchActionNames(context.Background(), "env", "flow")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "Compose,Secured" {
		t.Fatalf("names %v", names)
	}
	if slept.Load() != 1 {
		t.Fatalf("sleeps %d", slept.Load())
	}
	runs, err := client.Runs(context.Background(), "env", "flow", "Failed", &since, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page2Hits.Load() != 1 || len(runs) != 2 {
		t.Fatalf("runs %d page2 %d", len(runs), page2Hits.Load())
	}
	if runs[0].Name != "run1" || runs[0].Status != "Failed" || runs[0].Trigger.Status != "Succeeded" {
		t.Fatalf("run1 %+v", runs[0])
	}
	if string(runs[0].Trigger.Input) != `{"host":"sharepoint"}` {
		t.Fatalf("trigger input %s", runs[0].Trigger.Input)
	}
	if len(runs[0].Actions) != 2 {
		t.Fatalf("actions %+v", runs[0].Actions)
	}
	compose := runs[0].Actions[0]
	if string(compose.Input) != `{"from":"blob"}` || string(compose.Output) != "3134" {
		t.Fatalf("compose input %s output %s", compose.Input, compose.Output)
	}
	secured := runs[0].Actions[1]
	if !strings.Contains(string(secured.Output), "secured") {
		t.Fatalf("secured output %s", secured.Output)
	}
	if runs[1].Trigger.Status != "Succeeded" || string(runs[1].Trigger.Input) != `{"kind": "run"}` {
		t.Fatalf("run2 trigger %+v", runs[1].Trigger)
	}
}

func TestRunsHonorsLimit(t *testing.T) {
	var page2 atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "page=2") {
			page2.Add(1)
			_, _ = io.WriteString(w, `{"value":[{"name":"run2","properties":{"status":"Succeeded","trigger":{"status":"Succeeded"}}}]}`)
			return
		}
		if strings.Contains(r.URL.Path, "/actions") {
			_, _ = io.WriteString(w, `{"value":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{
		  "value": [{"name":"run1","properties":{"status":"Succeeded","startTime":"2026-10-27T23:56:32Z","trigger":{"status":"Succeeded"}}}],
		  "nextLink": "`+srv.URL+`/runs?page=2"
		}`)
	}))
	t.Cleanup(srv.Close)

	client := NewClient("tok", false)
	client.baseURL = srv.URL
	runs, err := client.Runs(context.Background(), "env", "flow", "Succeeded", nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Name != "run1" || page2.Load() != 0 {
		t.Fatalf("runs %+v page2 %d", runs, page2.Load())
	}
}

func TestAdminRoute(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"properties":{"definition":{"actions":{}}}}`)
	}))
	t.Cleanup(srv.Close)
	client := NewClient("tok", true)
	client.baseURL = srv.URL
	if _, err := client.FetchActionNames(context.Background(), "env", "flow"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "/scopes/admin/environments/env/flows/flow") {
		t.Fatalf("path %s", path)
	}
}

func TestUseFlowAuth(t *testing.T) {
	if !useFlowAuth("https://api.flow.microsoft.com/providers/runs", "https://api.flow.microsoft.com") {
		t.Fatal("expected flow host to send auth")
	}
	if useFlowAuth("https://blob.example/content?sig=secret", "https://api.flow.microsoft.com") {
		t.Fatal("blob host should not send the flow token")
	}
}
