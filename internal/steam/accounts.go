package steam

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

const maxSteamConfigBytes = 8 << 20

var accountNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

var accountsPath = []string{"installconfigstore", "software", "valve", "steam", "accounts"}

// SteamCMDDataDirs lists where SteamCMD may keep config/config.vdf for the given executable.
func SteamCMDDataDirs(executable string) []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if executable != "" {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		dirs = append(dirs, filepath.Dir(executable))
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".steam", "root"),
			filepath.Join(home, ".steam", "steam"),
			filepath.Join(home, ".local", "share", "Steam"),
			filepath.Join(home, "Steam"),
		)
		if runtime.GOOS == "darwin" {
			dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "Steam"))
		}
	}
	return dirs
}

// SteamCMDCachedAccounts returns account names SteamCMD has saved logins for. It never returns tokens.
func SteamCMDCachedAccounts(executable string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	var errs []error
	for _, dir := range SteamCMDDataDirs(executable) {
		path := filepath.Join(dir, "config", "config.vdf")
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		} else {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		data, err := readCapped(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		found, err := CachedAccountNames(data)
		if err != nil {
			errs = append(errs, err)
		}
		names = append(names, found...)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 {
		return []string{}, errors.Join(errs...)
	}
	return names, nil
}

func readCapped(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSteamConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSteamConfigBytes {
		return nil, errors.New("SteamCMD config.vdf is too large to read")
	}
	return data, nil
}

// CachedAccountNames extracts the block keys under InstallConfigStore/Software/Valve/Steam/Accounts.
func CachedAccountNames(data []byte) ([]string, error) {
	tokens, err := vdfTokens(string(data))
	var names []string
	var stack []string
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token.brace && token.text == "}" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if token.brace {
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].brace && tokens[i+1].text == "{" {
			if slices.Equal(stack, accountsPath) && accountNamePattern.MatchString(token.text) {
				names = append(names, token.text)
			}
			stack = append(stack, strings.ToLower(token.text))
			i++
			continue
		}
		// Skip the value of a key/value pair.
		i++
	}
	return names, err
}

type vdfToken struct {
	text  string
	brace bool
}

// vdfTokens splits KeyValues text into strings and braces, dropping comments and [$OS] conditions.
func vdfTokens(text string) ([]vdfToken, error) {
	var tokens []vdfToken
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case strings.HasPrefix(text[i:], "//"):
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				return tokens, nil
			}
			i += end
		case c == '{' || c == '}':
			tokens = append(tokens, vdfToken{text: string(c), brace: true})
			i++
		case c == '"':
			var value strings.Builder
			i++
			for ; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' && i+1 < len(text) {
					i++
				}
				value.WriteByte(text[i])
			}
			if i >= len(text) {
				return tokens, errors.New("SteamCMD config.vdf has an unterminated string")
			}
			i++
			tokens = append(tokens, vdfToken{text: value.String()})
		case c == '[':
			end := strings.IndexByte(text[i:], ']')
			if end < 0 {
				return tokens, errors.New("SteamCMD config.vdf has an unterminated condition")
			}
			i += end + 1
		default:
			start := i
			for i < len(text) && !strings.ContainsRune(" \t\r\n{}\"", rune(text[i])) {
				i++
			}
			tokens = append(tokens, vdfToken{text: text[start:i]})
		}
	}
	return tokens, nil
}
