package flow

import "testing"

func TestActionNames(t *testing.T) {
	body := []byte(`{
	  "properties": {
	    "definition": {
	      "triggers": { "When_item": { "type": "Request" } },
	      "actions": {
	        "Init": { "type": "InitializeVariable" },
	        "Check": {
	          "type": "If",
	          "actions": { "Inside": { "type": "Compose" } },
	          "else": { "actions": { "ElseOne": { "type": "Compose" } } }
	        },
	        "Route": {
	          "type": "Switch",
	          "cases": { "A": { "actions": { "CaseA": { "type": "Compose" } } } },
	          "default": { "actions": { "DefaultA": { "type": "Compose" } } }
	        },
	        "Apply_to_each": {
	          "type": "Foreach",
	          "actions": { "Child": { "type": "Compose" } }
	        },
	        "After": { "type": "Compose" }
	      }
	    }
	  }
	}`)
	got, err := ActionNames(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Init", "Check", "Inside", "ElseOne", "Route", "CaseA", "DefaultA", "Apply_to_each", "Child", "After"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestActionNamesStringDefinition(t *testing.T) {
	body := []byte(`{"properties":{"definition":"{\"actions\":{\"Only\":{}}}"}}`)
	got, err := ActionNames(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "Only" {
		t.Fatalf("got %v", got)
	}
}

func TestColumnOrder(t *testing.T) {
	got := ColumnOrder([]string{"Init", "Compose"}, [][]string{{"Compose", "Legacy"}})
	want := []string{"Trigger", "Init", "Compose", "Legacy"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func TestRunFilter(t *testing.T) {
	got := RunFilter("Failed", "2026-10-01T00:00:00Z", "2026-10-28T00:00:00Z")
	want := "status eq 'Failed' and startTime ge 2026-10-01T00:00:00Z and startTime lt 2026-10-28T00:00:00Z"
	if got != want {
		t.Fatalf("got %s", got)
	}
}
