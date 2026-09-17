// Package jsonl is the append-only JSON Lines evidence store.
package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/marcus/frost/internal/evidence"
)

const maxLine = 4 * 1024 * 1024

// Store appends records to one file and reads them back in order.
type Store struct {
	path string
}

var _ evidence.Store = (*Store)(nil)

// Open prepares a store at path. With create false the file must already
// exist; with create true a missing file is created on first Append.
func Open(path string, create bool) (*Store, error) {
	if path == "" {
		return nil, errors.New("evidence path is required")
	}
	if _, err := os.Stat(path); err != nil {
		if !create || !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return &Store{path: path}, nil
}

// Path returns the file backing the store.
func (s *Store) Path() string { return s.path }

// Append writes one record as a single line and fsyncs before returning, so
// a later failure cannot discard a billable result.
func (s *Store) Append(r evidence.Record) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = evidence.SchemaVersion
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if bytes.ContainsRune(b, '\n') {
		return errors.New("record encodes to more than one line")
	}
	if len(b) > maxLine {
		return fmt.Errorf("record is %d bytes, over the %d byte limit", len(b), maxLine)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Each calls fn for every record in file order. It stops at the first error.
func (s *Store) Each(fn func(evidence.Record) error) error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var r evidence.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("%s line %d: %w", s.path, line, err)
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return sc.Err()
}
