package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestConcurrentEditsDoNotOverwriteEachOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	if _, err := Initialize(InitOptions{Path: path, Name: "Original", Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := edit(path, func(root *yaml.Node, _ *Document) error {
			close(entered)
			<-resume
			mappingValue(mappingValue(root, "project"), "name").Value = "first"
			return nil
		})
		firstDone <- err
	}()
	select {
	case <-entered:
	case err := <-firstDone:
		t.Fatalf("first edit did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first edit did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := edit(path, func(root *yaml.Node, _ *Document) error {
			mappingValue(mappingValue(root, "project"), "name").Value = "second"
			return nil
		})
		secondDone <- err
	}()
	var secondErr error
	select {
	case secondErr = <-secondDone:
	case <-time.After(5 * time.Second):
		close(resume)
		<-firstDone
		t.Fatal("overlapping edit did not return promptly")
	}
	close(resume)
	if err := <-firstDone; err != nil {
		t.Fatalf("first edit failed: %v", err)
	}
	if secondErr == nil {
		t.Fatal("overlapping edit succeeded while another edit was in progress")
	}
	doc, err := Load(path)
	if err != nil || doc.Config.Project.Name != "first" {
		t.Fatalf("first edit lost: %#v, %v", doc, err)
	}
}

func TestEditRejectsConfigurationChangedByAnotherProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	if _, err := Initialize(InitOptions{Path: path, Name: "Original", Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := append(bytes.Clone(original), []byte("\n# changed in another editor\n")...)
	var externalErr error
	_, err = edit(path, func(_ *yaml.Node, _ *Document) error {
		externalErr = os.WriteFile(path, external, 0o600)
		return externalErr
	})
	if externalErr != nil {
		t.Skipf("platform prevented the competing write: %v", externalErr)
	}
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("edit did not report the competing write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, external) {
		t.Fatalf("competing change was lost: %q, %v", got, err)
	}
}

func TestEditRejectsConfigurationReplacedByAnotherProgram(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steam-release.yaml")
	if _, err := Initialize(InitOptions{Path: path, Name: "Original", Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement.yaml")
	if err := os.WriteFile(replacement, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var externalErr error
	_, err = edit(path, func(_ *yaml.Node, _ *Document) error {
		externalErr = os.Rename(replacement, path)
		return externalErr
	})
	if externalErr != nil {
		t.Skipf("platform prevented the competing replacement: %v", externalErr)
	}
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("edit did not report the competing replacement: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("replacement was modified: %q, %v", got, err)
	}
}

func TestForceInitializeDoesNotInterruptEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	options := InitOptions{Path: path, Name: "Original", Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}
	if _, err := Initialize(options); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := edit(path, func(root *yaml.Node, _ *Document) error {
			close(entered)
			<-resume
			mappingValue(mappingValue(root, "project"), "name").Value = "Edited"
			return nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("edit did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("edit did not start")
	}
	options.Force = true
	options.Name = "Forced"
	forceDone := make(chan error, 1)
	go func() {
		_, err := Initialize(options)
		forceDone <- err
	}()
	var forceErr error
	select {
	case forceErr = <-forceDone:
	case <-time.After(5 * time.Second):
		close(resume)
		<-done
		t.Fatal("force initialization did not return promptly")
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if forceErr == nil {
		t.Fatal("force initialization succeeded during an edit")
	}
	doc, err := Load(path)
	if err != nil || doc.Config.Project.Name != "Edited" {
		t.Fatalf("edit was lost: %#v, %v", doc, err)
	}
}

func TestEditPreservesFileIdentityAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steam-release.yaml")
	if _, err := Initialize(InitOptions{Path: path, Name: "Original", Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.yaml")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	link := filepath.Join(dir, "symlink.yaml")
	editPath := alias
	if err := os.Symlink(path, link); err != nil {
		t.Logf("symlinks unavailable; testing the hard link: %v", err)
	} else {
		editPath = link
	}
	_, err = edit(editPath, func(root *yaml.Node, _ *Document) error {
		mappingValue(mappingValue(root, "project"), "name").Value = "Edited"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("file identity or permissions changed: %v, %v, %v", before, after, err)
	}
	if editPath == link {
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symbolic link changed: %v", err)
		}
	}
	doc, err := Load(alias)
	if err != nil || doc.Config.Project.Name != "Edited" {
		t.Fatalf("hard link did not retain the edit: %#v, %v", doc, err)
	}
}
