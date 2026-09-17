package jsonl

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/frost/internal/evidence"
	"github.com/marcus/frost/internal/router"
)

func TestAppendAndEach(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	s, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i, text := range []string{"fix typo", "prove queue\nlinearizable"} {
		r := evidence.Record{
			RecordedAt:     now,
			ProgramVersion: "test",
			CaseID:         "c" + string(rune('0'+i)),
			Task:           router.Task{Text: text},
			Assessment:     router.Assessment{Provider: "typesafe", Nouls: map[string]float64{"missing_context": 0.1}},
			RawResponse:    json.RawMessage(`{"ok":true}`),
		}
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	// Blank lines are tolerated.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("\n\n")
	f.Close()

	var got []evidence.Record
	if err := s.Each(func(r evidence.Record) error { got = append(got, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records", len(got))
	}
	if got[0].SchemaVersion != evidence.SchemaVersion || got[0].CaseID != "c0" {
		t.Errorf("first record mismatch: %+v", got[0])
	}
	if got[1].Task.Text != "prove queue\nlinearizable" {
		t.Errorf("newline in text did not round-trip: %q", got[1].Task.Text)
	}
	if string(got[1].RawResponse) != `{"ok":true}` {
		t.Errorf("raw response mismatch: %s", got[1].RawResponse)
	}
}

func TestOpenMissingWithoutCreate(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "absent.jsonl"), false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestEachStopsOnCallbackError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	s, _ := Open(path, true)
	_ = s.Append(evidence.Record{CaseID: "a"})
	_ = s.Append(evidence.Record{CaseID: "b"})
	want := errors.New("stop")
	n := 0
	err := s.Each(func(evidence.Record) error { n++; return want })
	if !errors.Is(err, want) || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestEachRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte("{\"schema_version\":1}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := Open(path, false)
	if err := s.Each(func(evidence.Record) error { return nil }); err == nil {
		t.Fatal("expected error for malformed line")
	}
}
