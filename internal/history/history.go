// Package history stores local desktop release attempts and their GUI visibility.
package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

const Limit = 50
const LogLimit = 128 << 10

type Request struct {
	Action        string   `json:"action"`
	ConfigPath    string   `json:"config_path"`
	ProjectRoot   string   `json:"project_root"`
	Profile       string   `json:"profile"`
	Targets       []string `json:"targets"`
	Username      string   `json:"username"`
	Preview       bool     `json:"preview"`
	SetLive       string   `json:"set_live"`
	Notifications bool     `json:"notifications,omitempty"`
	RetryID       string   `json:"retry_id,omitempty"`
	ReuseTargets  []string `json:"-"`
	// Fingerprints are restored from trusted local history, not from the GUI.
	TargetFingerprints map[string]string `json:"-"`
	UploadFingerprint  string            `json:"-"`
	UnverifiedTargets  []string          `json:"-"`
	RetrySnapshot      *RetrySnapshot    `json:"-"`
}

type RetrySnapshot struct {
	ConfigPath string
	ConfigHash string
	Root       string
	Commit     string
	SourceHash string
}

type Record struct {
	ID                 string            `json:"id"`
	Request            Request           `json:"request"`
	Project            string            `json:"project"`
	Root               string            `json:"root"`
	AppID              uint64            `json:"app_id"`
	StartedAt          time.Time         `json:"started_at"`
	FinishedAt         *time.Time        `json:"finished_at,omitempty"`
	DurationMS         int64             `json:"duration_ms"`
	Status             string            `json:"status"`
	Error              string            `json:"error,omitempty"`
	ConfigHash         string            `json:"config_hash"`
	Commit             string            `json:"commit,omitempty"`
	SourceHash         string            `json:"source_hash,omitempty"`
	Version            string            `json:"version,omitempty"`
	Dirty              bool              `json:"dirty"`
	BuildID            string            `json:"build_id,omitempty"`
	CompletedTargets   []string          `json:"completed_targets"`
	BuildReady         bool              `json:"build_ready"`
	CustomUploadScript bool              `json:"custom_upload_script,omitempty"`
	TargetFingerprints map[string]string `json:"target_fingerprints,omitempty"`
	UploadFingerprint  string            `json:"upload_fingerprint,omitempty"`
	Logs               string            `json:"logs"`
}

func openStore() (*sql.DB, error) {
	db, err := localstore.Open()
	if err != nil {
		return nil, err
	}
	err = localstore.Migrate(db, "history.json", func(tx *sql.Tx) error {
		directory, err := localstore.Directory()
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.Join(directory, "history.json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var records []Record
		if err := json.Unmarshal(contents, &records); err != nil {
			return fmt.Errorf("import local release history: %w", err)
		}
		if len(records) > Limit {
			records = records[:Limit]
		}
		// Legacy records are newest first; insert backwards to retain their order.
		for i := len(records) - 1; i >= 0; i-- {
			payload, err := json.Marshal(records[i])
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO release_history(id, config_path, project_root, payload)
				VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
				records[i].ID, records[i].Request.ConfigPath, records[i].Root, string(payload)); err != nil {
				return err
			}
		}
		return prune(tx)
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func List(configPath, root string) ([]Record, error) {
	return list(configPath, root, false)
}

// ListVisible returns attempts that have not been cleared from the GUI.
func ListVisible(configPath, root string) ([]Record, error) {
	return list(configPath, root, true)
}

func list(configPath, root string, visibleOnly bool) ([]Record, error) {
	db, err := openStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT payload FROM release_history
		WHERE config_path = ? AND project_root = ? AND (? = 0 OR hidden = 0)
		ORDER BY sequence DESC`, configPath, root, visibleOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selected := []Record{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var record Record
		if err := json.Unmarshal([]byte(payload), &record); err != nil {
			return nil, fmt.Errorf("read local release history: %w", err)
		}
		selected = append(selected, record)
	}
	return selected, rows.Err()
}

// Hide clears a project's GUI history while retaining every stored record.
func Hide(configPath, root string) error {
	return hide(configPath, root, nil)
}

// HideRecords clears only the selected attempts. Unselected attempts remain
// visible, including releases that finish after the selection was made.
func HideRecords(configPath, root string, recordIDs []string) error {
	if len(recordIDs) == 0 {
		return nil
	}
	return hide(configPath, root, recordIDs)
}

func hide(configPath, root string, recordIDs []string) error {
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	query := "UPDATE release_history SET hidden = 1 WHERE config_path = ? AND project_root = ?"
	args := []any{configPath, root}
	if recordIDs != nil {
		query += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(recordIDs)), ",") + ")"
		for _, id := range recordIDs {
			args = append(args, id)
		}
	}
	_, err = db.Exec(query, args...)
	return err
}

func Find(id string) (Record, error) {
	db, err := openStore()
	if err != nil {
		return Record{}, err
	}
	defer db.Close()
	var payload string
	err = db.QueryRow("SELECT payload FROM release_history WHERE id = ?", id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("release attempt was not found in local history")
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return Record{}, fmt.Errorf("read local release history: %w", err)
	}
	return record, nil
}

func Save(record Record) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Moving an updated attempt to the front matches the existing history order.
	// An update preserves visibility so cleared attempts cannot reappear.
	if _, err := tx.Exec(`INSERT INTO release_history(id, config_path, project_root, payload)
		VALUES (?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET
		sequence = excluded.sequence, config_path = excluded.config_path,
		project_root = excluded.project_root, payload = excluded.payload`,
		record.ID, record.Request.ConfigPath, record.Root, string(payload)); err != nil {
		return err
	}
	if err := prune(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func prune(tx *sql.Tx) error {
	// Cleared records remain available in SQLite even as new attempts arrive.
	_, err := tx.Exec(`DELETE FROM release_history WHERE hidden = 0 AND sequence NOT IN
		(SELECT sequence FROM release_history WHERE hidden = 0 ORDER BY sequence DESC LIMIT ?)`, Limit)
	return err
}

func ConfigHash(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}

func GitMetadata(root string) (commit, version string, dirty bool) {
	return GitMetadataContext(context.Background(), root)
}

func GitMetadataContext(parent context.Context, root string) (commit, version string, dirty bool) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	git := func(args ...string) string {
		output, err := sourceGitCommand(ctx, root, args...).Output()
		if err != nil || len(output) > 4096 {
			return ""
		}
		return strings.TrimSpace(string(output))
	}
	commit = git("rev-parse", "HEAD")
	if commit == "" {
		return
	}
	version = git("describe", "--tags", "--always", "--dirty")
	dirty = strings.HasSuffix(version, "-dirty")
	return
}

var sensitiveLine = regexp.MustCompile(`(?im)((?:password|passwd|steam[_ ]?guard[_ ]?(?:code)?|access[_ ]?token|refresh[_ ]?token)\s*[:=]\s*)[^\r\n]+`)

// Sanitize runs before truncation so echoed responses, even across chunks,
// cannot leave secret prefixes in the persisted log.
func Sanitize(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = sensitiveLine.ReplaceAllString(value, "${1}[redacted]")
	if len(value) > LogLimit {
		value = "[earlier output omitted]\n" + value[len(value)-LogLimit:]
	}
	return value
}

func RetryRequest(record Record, notifications bool) (Request, error) {
	return retryRequest(record, notifications, true)
}

// PrepareRetryRequest defers the expensive source check to the caller, which
// must run VerifyRetrySnapshotContext while holding the project lock.
func PrepareRetryRequest(record Record, notifications bool) (Request, error) {
	return retryRequest(record, notifications, false)
}

func retryRequest(record Record, notifications, verifySource bool) (Request, error) {
	if record.Status != "failed" && record.Status != "cancelled" {
		return Request{}, fmt.Errorf("only failed or cancelled releases can be retried")
	}
	if record.Request.Action != "build" && record.Request.Action != "ship" && record.Request.Action != "upload" {
		return Request{}, fmt.Errorf("this operation cannot be retried")
	}
	if record.CustomUploadScript && (record.Request.Action == "ship" || record.Request.Action == "upload") {
		return Request{}, fmt.Errorf("cannot verify upload contents referenced by existing_vdf; start a new operation after reviewing the script")
	}
	snapshot := RetrySnapshot{ConfigPath: record.Request.ConfigPath, ConfigHash: record.ConfigHash, Root: record.Root, Commit: record.Commit, SourceHash: record.SourceHash}
	var err error
	if verifySource {
		err = VerifyRetrySnapshot(snapshot)
	} else {
		err = verifyRetryMetadata(context.Background(), snapshot)
	}
	if err != nil {
		return Request{}, err
	}
	request := record.Request
	request.Notifications = notifications
	request.RetryID = record.ID
	request.RetrySnapshot = &snapshot
	request.ReuseTargets = nil
	request.TargetFingerprints = nil
	request.UploadFingerprint = ""
	request.UnverifiedTargets = nil
	// A byte-for-byte artifact checkpoint does not prove that a later target
	// will use the same source. Rebuild completed targets when the original
	// attempt had no verifiable Git source checkpoint.
	verifiedSource := snapshot.Commit != "" && validFingerprint(snapshot.SourceHash)
	for _, target := range record.CompletedTargets {
		fingerprint := record.TargetFingerprints[target]
		if !verifiedSource || !validFingerprint(fingerprint) {
			request.UnverifiedTargets = append(request.UnverifiedTargets, target)
			continue
		}
		request.ReuseTargets = append(request.ReuseTargets, target)
		if request.TargetFingerprints == nil {
			request.TargetFingerprints = make(map[string]string)
		}
		request.TargetFingerprints[target] = fingerprint
	}
	if record.BuildReady && request.Action == "ship" {
		if !validFingerprint(record.UploadFingerprint) {
			return Request{}, fmt.Errorf("this release has no artifact checkpoint; start a new build and deploy")
		}
		request.Action = "upload"
		request.UploadFingerprint = record.UploadFingerprint
		request.ReuseTargets = nil
		request.TargetFingerprints = nil
		request.UnverifiedTargets = nil
	} else if request.Action == "upload" {
		if !validFingerprint(record.UploadFingerprint) {
			return Request{}, fmt.Errorf("this upload has no artifact checkpoint; start a new upload")
		}
		request.UploadFingerprint = record.UploadFingerprint
	}
	return request, nil
}

// VerifyRetrySnapshot is repeated under the project lock before a retry runs.
func VerifyRetrySnapshot(snapshot RetrySnapshot) error {
	return VerifyRetrySnapshotContext(context.Background(), snapshot)
}

// VerifyRetrySnapshotContext repeats the source check under the project lock.
func VerifyRetrySnapshotContext(ctx context.Context, snapshot RetrySnapshot) error {
	if err := verifyRetryMetadata(ctx, snapshot); err != nil {
		return err
	}
	if snapshot.SourceHash != "" {
		sourceHash, err := SourceHashContext(ctx, snapshot.Root)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("cannot verify source files since this attempt; start a new release")
		}
		if sourceHash != snapshot.SourceHash {
			return fmt.Errorf("source files changed or the source checkpoint is incompatible; start a new release")
		}
	}
	return nil
}

func verifyRetryMetadata(ctx context.Context, snapshot RetrySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash, err := ConfigHash(snapshot.ConfigPath)
	if err != nil {
		return err
	}
	if hash != snapshot.ConfigHash {
		return fmt.Errorf("configuration changed since this attempt; start a new release")
	}
	commit, _, _ := GitMetadataContext(ctx, snapshot.Root)
	if err := ctx.Err(); err != nil {
		return err
	}
	if commit != snapshot.Commit {
		return fmt.Errorf("Git commit changed since this attempt; start a new release")
	}
	return nil
}

func validFingerprint(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
