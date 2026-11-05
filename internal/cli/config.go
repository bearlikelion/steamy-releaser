package cli

import (
	"fmt"
	"strings"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/spf13/cobra"
)

func (a *application) configCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Edit release profiles, targets, and build options"}
	command.AddCommand(a.addProfileCommand(), a.addConfigTargetCommand(), a.setBuildOptionsCommand())
	return command
}

func (a *application) addProfileCommand() *cobra.Command {
	var appID uint64
	var targets []string
	var buildOptions buildOptionsFlags
	command := &cobra.Command{
		Use:   "add-profile NAME",
		Short: "Add a profile while preserving existing YAML settings and comments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			patch, err := buildOptions.patch(cmd)
			if err != nil {
				return err
			}
			if err := requireSingleBuildTarget(patch, len(targets)); err != nil {
				return err
			}
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			profile := config.Profile{AppID: appID, Targets: map[string]config.Target{}}
			for _, value := range targets {
				target, err := parseInitTarget(value)
				if err != nil {
					return err
				}
				if _, exists := profile.Targets[target.Name]; exists {
					return fmt.Errorf("duplicate target %q", target.Name)
				}
				applyInitBuildOptions(&target, patch)
				profile.Targets[target.Name] = config.TargetFromInit(loaded.Document.Config.Project.Engine.Type, target)
			}
			name := strings.TrimSpace(args[0])
			document, err := config.AddProfile(loaded.Document.Path, name, profile)
			if err != nil {
				return err
			}
			return a.writeResult(map[string]any{"config": document.Path, "profile": name})
		},
	}
	command.Flags().Uint64Var(&appID, "app-id", 0, "Steam App ID for the new profile")
	command.Flags().StringArrayVar(&targets, "target", nil, "target CSV: name,depot_id,path[,engine_setting] (repeatable)")
	buildOptions.register(command, false)
	_ = command.MarkFlagRequired("app-id")
	_ = command.MarkFlagRequired("target")
	return command
}

func (a *application) addConfigTargetCommand() *cobra.Command {
	var value, architecture string
	var buildOptions buildOptionsFlags
	command := &cobra.Command{
		Use:   "add-target [PROFILE]",
		Short: "Add a target to the selected profile while preserving existing YAML settings and comments",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			patch, err := buildOptions.patch(cmd)
			if err != nil {
				return err
			}
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			selection, err := loaded.Document.Select(profileArgument(args), nil)
			if err != nil {
				return err
			}
			target, err := parseInitTarget(value)
			if err != nil {
				return err
			}
			target.Architecture = strings.TrimSpace(architecture)
			applyInitBuildOptions(&target, patch)
			document, err := config.AddTarget(loaded.Document.Path, selection.Name, target.Name,
				config.TargetFromInit(loaded.Document.Config.Project.Engine.Type, target))
			if err != nil {
				return err
			}
			return a.writeResult(map[string]any{"config": document.Path, "profile": selection.Name, "target": target.Name})
		},
	}
	command.Flags().StringVar(&value, "target", "", "target CSV: name,depot_id,path[,engine_setting]")
	command.Flags().StringVar(&architecture, "architecture", "", "target architecture, e.g. x86_64 or arm64")
	buildOptions.register(command, false)
	_ = command.MarkFlagRequired("target")
	return command
}

func (a *application) setBuildOptionsCommand() *cobra.Command {
	var options buildOptionsFlags
	command := &cobra.Command{
		Use:   "set-build-options TARGET [PROFILE]",
		Short: "Replace a target's build arguments or environment while preserving other YAML settings",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			patch, err := options.patch(cmd)
			if err != nil {
				return err
			}
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			selection, err := loaded.Document.Select(profileArgument(args[1:]), []string{args[0]})
			if err != nil {
				return err
			}
			document, err := config.UpdateTargetBuildOptions(loaded.Document.Path, selection.Name, args[0], patch)
			if err != nil {
				return err
			}
			return a.writeResult(map[string]any{"config": document.Path, "profile": selection.Name, "target": args[0]})
		},
	}
	options.register(command, true)
	return command
}

type buildOptionsFlags struct {
	args, env           []string
	clearArgs, clearEnv bool
}

func (o *buildOptionsFlags) register(cmd *cobra.Command, clear bool) {
	cmd.Flags().StringArrayVar(&o.args, "build-arg", nil, "one extra build argument token (repeatable; use --build-arg=--flag)")
	cmd.Flags().StringArrayVar(&o.env, "build-env", nil, "build environment override NAME=VALUE (repeatable)")
	if clear {
		cmd.Flags().BoolVar(&o.clearArgs, "clear-build-args", false, "remove the target's extra build arguments")
		cmd.Flags().BoolVar(&o.clearEnv, "clear-build-env", false, "remove the target's environment overrides")
	}
}

func (o *buildOptionsFlags) patch(cmd *cobra.Command) (config.BuildOptionsPatch, error) {
	patch := config.BuildOptionsPatch{}
	if o.clearArgs && cmd.Flags().Changed("build-arg") || o.clearEnv && cmd.Flags().Changed("build-env") {
		return patch, fmt.Errorf("cannot supply and clear the same build option group")
	}
	if cmd.Flags().Changed("build-arg") || o.clearArgs {
		patch.Args = &o.args
	}
	if cmd.Flags().Changed("build-env") || o.clearEnv {
		env := map[string]string{}
		for _, value := range o.env {
			name, content, ok := strings.Cut(value, "=")
			if !ok || name == "" {
				return patch, fmt.Errorf("--build-env must be NAME=VALUE")
			}
			if _, exists := env[name]; exists {
				return patch, fmt.Errorf("--build-env repeats name %q", name)
			}
			env[name] = content
		}
		patch.Env = &env
	}
	return patch, nil
}

func requireSingleBuildTarget(patch config.BuildOptionsPatch, count int) error {
	if (patch.Args != nil || patch.Env != nil) && count != 1 {
		return fmt.Errorf("--build-arg and --build-env require exactly one --target; use config set-build-options to configure targets individually")
	}
	return nil
}

func applyInitBuildOptions(target *config.InitTarget, patch config.BuildOptionsPatch) {
	if patch.Args != nil {
		target.BuildArgs = *patch.Args
	}
	if patch.Env != nil {
		target.BuildEnv = *patch.Env
	}
}
