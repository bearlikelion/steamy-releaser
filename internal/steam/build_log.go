package steam

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/thornity/steam-release-tool/internal/safefile"
)

const maxSteamBuildLog = 4 << 20

var managedRunDirectory = regexp.MustCompile(`^run-[0-9a-f]{32}$`)

// generatedBuildLog holds the uniquely created default output directory open
// while SteamCMD runs. Shared or user-supplied output paths are not evidence
// of the current upload because they may contain a previous build's log.
type generatedBuildLog struct {
	project   *os.Root
	directory *os.Root
	relative  string
	identity  fs.FileInfo
}

func openGeneratedBuildLog(projectPath, outputPath string) (*generatedBuildLog, error) {
	relative, err := filepath.Rel(projectPath, outputPath)
	if err != nil || !filepath.IsLocal(relative) || filepath.Dir(relative) != filepath.Join(".steam-releaser", "output") || !managedRunDirectory.MatchString(filepath.Base(relative)) {
		return nil, fmt.Errorf("invalid generated Steam output path")
	}
	project, err := os.OpenRoot(projectPath)
	if err != nil {
		return nil, err
	}
	closeProject := true
	defer func() {
		if closeProject {
			_ = project.Close()
		}
	}()
	info, err := project.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || !os.SameFile(info, info) {
		return nil, fmt.Errorf("generated Steam output is not an identifiable directory")
	}
	directory, err := project.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	closeDirectory := true
	defer func() {
		if closeDirectory {
			_ = directory.Close()
		}
	}()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("generated Steam output changed before upload")
	}
	file, err := directory.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := file.Readdirnames(1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) != 0 {
		return nil, fmt.Errorf("generated Steam output was not empty before upload")
	}
	closeProject, closeDirectory = false, false
	return &generatedBuildLog{project: project, directory: directory, relative: relative, identity: info}, nil
}

func (log *generatedBuildLog) Close() {
	_ = log.directory.Close()
	_ = log.project.Close()
}

// Read returns only a bounded, stable, regular log from the same fresh
// directory. Missing logs are permitted because SteamCMD may use the console.
func (log *generatedBuildLog) Read(appID uint64) (string, bool, error) {
	current, err := log.project.Lstat(log.relative)
	if err != nil || !current.IsDir() || current.Mode()&fs.ModeSymlink != 0 || !os.SameFile(log.identity, current) {
		return "", false, fmt.Errorf("generated Steam output changed during upload")
	}
	name := fmt.Sprintf("app_build_%d.log", appID)
	info, err := log.directory.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("Steam build log is not a regular file")
	}
	file, err := safefile.OpenCandidate(log.directory, name)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", false, fmt.Errorf("Steam build log changed before reading")
	}
	if opened.Size() > maxSteamBuildLog {
		return "", false, fmt.Errorf("Steam build log exceeds the capture limit")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxSteamBuildLog+1))
	if err != nil {
		return "", false, err
	}
	after, err := file.Stat()
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maxSteamBuildLog || after.Size() != opened.Size() || after.Mode() != opened.Mode() || !after.ModTime().Equal(opened.ModTime()) {
		return "", false, fmt.Errorf("Steam build log changed while reading")
	}
	return string(contents), true, nil
}
