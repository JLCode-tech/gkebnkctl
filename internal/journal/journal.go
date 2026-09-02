package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// DefaultJournalPath is the standard location for workspace journal.
const DefaultJournalPath = "artifacts/journal.jsonl"

// Entry represents a single audit or operational journal record.
type Entry struct {
	Timestamp string            `json:"timestamp"`
	Actor     string            `json:"actor"`
	Action    string            `json:"action"`
	Cluster   string            `json:"cluster,omitempty"`
	Message   string            `json:"message"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Append writes a new Entry to the journal file at path.
func Append(path string, e Entry) error {
	if path == "" {
		path = DefaultJournalPath
	}

	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if e.Actor == "" {
		e.Actor = currentUsername()
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 G302 -- journal path is workspace-local
	if err != nil {
		return fmt.Errorf("open journal %s: %w", path, err)
	}
	defer f.Close()

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal journal entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write journal entry: %w", err)
	}

	return nil
}

// List reads all entries from the journal file.
func List(path string) ([]Entry, error) {
	if path == "" {
		path = DefaultJournalPath
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}

	f, err := os.Open(path) // #nosec G304 -- journal path is workspace-local
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", path, err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan journal %s: %w", path, err)
	}

	return entries, nil
}

// RenderText writes formatted journal entries to w.
func RenderText(w io.Writer, entries []Entry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No journal entries found.")
		return
	}

	fmt.Fprintf(w, "%-20s  %-12s  %-10s  %s\n", "TIMESTAMP", "ACTOR", "ACTION", "MESSAGE")
	fmt.Fprintf(w, "%s\n", "--------------------------------------------------------------------------------")
	for _, e := range entries {
		ts := e.Timestamp
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ts = t.Format("2006-01-02 15:04:05")
		}
		action := e.Action
		if action == "" {
			action = "NOTE"
		}
		fmt.Fprintf(w, "%-20s  %-12s  %-10s  %s\n", ts, e.Actor, action, e.Message)
	}
}

func currentUsername() string {
	u, err := user.Current()
	if err == nil && u.Username != "" {
		return u.Username
	}
	if envUser := os.Getenv("USER"); envUser != "" {
		return envUser
	}
	return "operator"
}
