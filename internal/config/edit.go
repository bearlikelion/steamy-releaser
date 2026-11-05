package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// AddProfile appends a named profile without discarding unrelated YAML settings or comments.
func AddProfile(path, name string, profile Profile) (*Document, error) {
	return edit(path, func(root *yaml.Node, current *Document) error {
		profiles := mappingValue(root, "profiles")
		if profiles == nil {
			profiles = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			release := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for _, key := range []string{"app_id", "description", "build_output", "existing_vdf", "targets", "post_build"} {
				moveMappingPair(root, release, key)
			}
			appendMapping(profiles, DefaultProfile, release)
			appendMapping(root, "profiles", profiles)
		}
		if mappingValue(profiles, name) != nil {
			return fmt.Errorf("profile %q already exists", name)
		}
		value := &yaml.Node{}
		if err := value.Encode(profile); err != nil {
			return err
		}
		appendMapping(profiles, name, value)
		return nil
	})
}

// AddTarget appends a target to either the short form or a named profile.
func AddTarget(path, profileName, name string, target Target) (*Document, error) {
	return edit(path, func(root *yaml.Node, current *Document) error {
		if _, exists := current.Config.Profiles[profileName]; !exists {
			return fmt.Errorf("unknown release profile %q", profileName)
		}
		profile := root
		if profiles := mappingValue(root, "profiles"); profiles != nil {
			profile = mappingValue(profiles, profileName)
		}
		if profile == nil {
			return fmt.Errorf("profile %q is missing from configuration", profileName)
		}
		targets := mappingValue(profile, "targets")
		if targets == nil {
			return fmt.Errorf("profile %q has no targets mapping", profileName)
		}
		if mappingValue(targets, name) != nil {
			return fmt.Errorf("target %q already exists in profile %q", name, profileName)
		}
		value := &yaml.Node{}
		if err := value.Encode(target); err != nil {
			return err
		}
		appendMapping(targets, name, value)
		return nil
	})
}

// BuildOptionsPatch replaces only the supplied option groups. A pointer to an
// empty list/map clears that group; nil leaves it unchanged.
type BuildOptionsPatch struct {
	Args *[]string
	Env  *map[string]string
}

func UpdateTargetBuildOptions(path, profileName, name string, patch BuildOptionsPatch) (*Document, error) {
	return edit(path, func(root *yaml.Node, current *Document) error {
		selection, err := current.Select(profileName, []string{name})
		if err != nil {
			return err
		}
		if current.Config.Project.Engine.Type == "prebuilt" {
			return fmt.Errorf("prebuilt targets do not have build options")
		}
		if patch.Args == nil && patch.Env == nil {
			return fmt.Errorf("supply build arguments or environment overrides to update")
		}
		profile := root
		if profiles := mappingValue(root, "profiles"); profiles != nil {
			profile = mappingValue(profiles, selection.Name)
		}
		target := mappingValue(mappingValue(profile, "targets"), name)
		if target == nil || target.Kind != yaml.MappingNode {
			return fmt.Errorf("target %q must be a YAML mapping to edit build options", name)
		}
		if target.Anchor != "" && hasAliasTo(root, target) {
			return fmt.Errorf("target %q is shared through a YAML anchor; give it an independent mapping before editing build options", name)
		}
		build := mappingValue(target, "build")
		if build == nil {
			build = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendMapping(target, "build", build)
		}
		if build.Kind != yaml.MappingNode {
			return fmt.Errorf("target %q build must be a YAML mapping to edit options", name)
		}
		if build.Anchor != "" && hasAliasTo(root, build) {
			return fmt.Errorf("target %q build is shared through a YAML anchor; give it an independent mapping before editing options", name)
		}
		if patch.Args != nil {
			if err := setOptionalMapping(build, "args", *patch.Args, len(*patch.Args) == 0); err != nil {
				return err
			}
		}
		if patch.Env != nil {
			if err := setOptionalMapping(build, "env", *patch.Env, len(*patch.Env) == 0); err != nil {
				return err
			}
		}
		return nil
	})
}

func hasAliasTo(node, target *yaml.Node) bool {
	if node.Kind == yaml.AliasNode && node.Alias == target {
		return true
	}
	for _, child := range node.Content {
		if hasAliasTo(child, target) {
			return true
		}
	}
	return false
}

func setOptionalMapping(mapping *yaml.Node, key string, value any, remove bool) error {
	node := &yaml.Node{}
	if !remove {
		if err := node.Encode(value); err != nil {
			return err
		}
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if remove {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
		} else {
			previous := mapping.Content[i+1]
			node.HeadComment, node.LineComment, node.FootComment = previous.HeadComment, previous.LineComment, previous.FootComment
			mapping.Content[i+1] = node
		}
		return nil
	}
	if !remove {
		appendMapping(mapping, key, node)
	}
	return nil
}

func edit(path string, change func(*yaml.Node, *Document) error) (result *Document, resultErr error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, unlock, err := openLockedConfiguration(absolute, os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("open configuration %s: %w", absolute, err)
	}
	defer func() {
		unlock()
		if err := file.Close(); err != nil && resultErr == nil {
			result, resultErr = nil, fmt.Errorf("close configuration %s: %w", absolute, err)
		}
	}()
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	// Decode and edit the same bytes; a second independent read can observe a
	// different version and apply a change using stale validation results.
	current, err := decode(absolute, bytes.NewReader(contents))
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configuration must contain a YAML mapping")
	}
	if err := change(document.Content[0], current); err != nil {
		return nil, err
	}
	updated, err := yaml.Marshal(&document)
	if err != nil {
		return nil, err
	}
	validated, err := decode(current.Path, bytes.NewReader(updated))
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	latest, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(contents, latest) {
		return nil, fmt.Errorf("configuration %s changed while it was being edited", current.Path)
	}
	if err := replaceConfigurationContents(file, current.Path, updated); err != nil {
		return nil, err
	}
	return validated, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func moveMappingPair(from, to *yaml.Node, key string) {
	for i := 0; i+1 < len(from.Content); i += 2 {
		if from.Content[i].Value == key {
			to.Content = append(to.Content, from.Content[i:i+2]...)
			from.Content = append(from.Content[:i], from.Content[i+2:]...)
			return
		}
	}
}

func appendMapping(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
