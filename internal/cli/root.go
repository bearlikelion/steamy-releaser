package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/service"
	"github.com/thornity/steam-release-tool/internal/steam"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type application struct {
	configPath     string
	projectRoot    string
	jsonOutput     bool
	verbose        bool
	nonInteractive bool
	timeout        time.Duration
	out            io.Writer
	errOut         io.Writer
}

func NewRootCommand(out, errOut io.Writer) *cobra.Command {
	app := &application{out: out, errOut: errOut}
	root := &cobra.Command{
		Use:           "steam-releaser",
		Short:         "Build and publish games to Steam from any desktop OS",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if app.timeout < 0 {
				return fmt.Errorf("--timeout must be zero or a positive duration")
			}
			return nil
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVarP(&app.configPath, "config", "c", "", "configuration file (discovered automatically when omitted)")
	root.PersistentFlags().StringVar(&app.projectRoot, "project-root", "", "local project directory override (does not edit the configuration)")
	root.PersistentFlags().BoolVar(&app.jsonOutput, "json", false, "write machine-readable JSON")
	root.PersistentFlags().BoolVarP(&app.verbose, "verbose", "v", false, "include command lines and detailed process output")
	root.PersistentFlags().BoolVar(&app.nonInteractive, "non-interactive", unattendedEnvironment(), "disable SteamCMD prompts and use its saved login token (defaults to true in CI)")
	root.PersistentFlags().DurationVar(&app.timeout, "timeout", 0, "maximum operation duration, e.g. 30m (zero disables the limit)")

	root.AddCommand(
		app.initCommand(),
		app.doctorCommand(),
		app.planCommand(),
		app.buildCommand(),
		app.uploadCommand(false),
		app.uploadCommand(true),
		app.authCommand(),
		app.configCommand(),
	)
	return root
}

func (a *application) initCommand() *cobra.Command {
	var options config.InitOptions
	var targetValues []string
	var buildOptions buildOptionsFlags
	command := &cobra.Command{
		Use:   "init",
		Short: "Create a generic Steam Releaser configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			patch, err := buildOptions.patch(cmd)
			if err != nil {
				return err
			}
			if err := requireSingleBuildTarget(patch, len(targetValues)); err != nil {
				return err
			}
			if a.configPath != "" {
				options.Path = a.configPath
			}
			for _, value := range targetValues {
				target, err := parseInitTarget(value)
				if err != nil {
					return err
				}
				applyInitBuildOptions(&target, patch)
				options.Targets = append(options.Targets, target)
			}
			document, err := config.Initialize(options)
			if err != nil {
				return err
			}
			return a.writeResult(map[string]any{"config": document.Path, "engine": document.Config.Project.Engine.Type})
		},
	}
	command.Flags().StringVar(&options.Path, "path", "steam-release.yaml", "configuration path")
	command.Flags().StringVar(&options.Name, "name", "", "project name (defaults to root directory name)")
	command.Flags().StringVar(&options.Root, "root", ".", "project root, relative to the configuration")
	command.Flags().StringVar(&options.Engine, "engine", "auto", "godot, unity, custom, prebuilt, or auto")
	command.Flags().Uint64Var(&options.AppID, "app-id", 0, "Steam App ID")
	command.Flags().StringVar(&options.Profile, "profile", config.DefaultProfile, "initial release profile name")
	command.Flags().StringVar(&options.SteamCMD, "steamcmd", "", "SteamCMD executable override")
	command.Flags().StringArrayVar(&targetValues, "target", nil, "target CSV: name,depot_id,path[,engine_setting]; path is a build output or prebuilt content directory (repeatable)")
	command.Flags().BoolVar(&options.Force, "force", false, "replace an existing configuration")
	buildOptions.register(command, false)
	_ = command.MarkFlagRequired("app-id")
	_ = command.MarkFlagRequired("target")
	return command
}

func (a *application) doctorCommand() *cobra.Command {
	var options service.RunOptions
	command := &cobra.Command{
		Use:   "doctor [PROFILE]",
		Short: "Check configuration, tools, engine support, and artifacts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			report, err := loaded.Doctor(profileArgument(args), options)
			if err != nil {
				return err
			}
			if err := a.writeResult(report); err != nil {
				return err
			}
			if !report.OK() {
				return fmt.Errorf("doctor found one or more blocking issues")
			}
			return nil
		},
	}
	addTargetFlag(command, &options)
	return command
}

func (a *application) planCommand() *cobra.Command {
	var run service.RunOptions
	var upload steam.UploadOptions
	command := &cobra.Command{
		Use:   "plan [PROFILE]",
		Short: "Print the build and upload plan without changing anything",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			plan, err := loaded.Plan(profileArgument(args), run, upload)
			if err != nil {
				return err
			}
			return a.writeResult(plan)
		},
	}
	addTargetFlag(command, &run)
	addScriptFlags(command, &upload)
	return command
}

func (a *application) buildCommand() *cobra.Command {
	var options service.RunOptions
	command := &cobra.Command{
		Use:   "build [PROFILE]",
		Short: "Build and validate selected targets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			ctx, cancel := a.operationContext(cmd)
			defer cancel()
			if err := loaded.Build(ctx, profileArgument(args), options); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return a.writeCompletion("build")
		},
	}
	addTargetFlag(command, &options)
	return command
}

func (a *application) uploadCommand(ship bool) *cobra.Command {
	var run service.RunOptions
	var upload steam.UploadOptions
	name, short := "upload", "Validate existing artifacts and upload them through SteamCMD"
	if ship {
		name, short = "ship", "Build, validate, and upload selected targets"
	}
	command := &cobra.Command{
		Use:   name + " [PROFILE]",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			var result steam.UploadResult
			upload.Interactive = !a.nonInteractive
			ctx, cancel := a.operationContext(cmd)
			defer cancel()
			if ship {
				result, err = loaded.Ship(ctx, profileArgument(args), run, upload)
			} else {
				result, err = loaded.Upload(ctx, profileArgument(args), run, upload)
			}
			if err != nil {
				return err
			}
			return a.writeResult(result)
		},
	}
	addTargetFlag(command, &run)
	addScriptFlags(command, &upload)
	command.Flags().StringVarP(&upload.Username, "username", "u", "", "Steam username (environment/cache fallback when omitted)")
	return command
}

func (a *application) authCommand() *cobra.Command {
	authCommand := &cobra.Command{Use: "auth", Short: "Manage SteamCMD authentication"}
	authCommand.AddCommand(&cobra.Command{
		Use:   "login USERNAME",
		Short: "Authenticate or verify SteamCMD's saved login token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			ctx, cancel := a.operationContext(cmd)
			defer cancel()
			if err := loaded.LoginWithOptions(ctx, args[0], !a.nonInteractive); err != nil {
				return err
			}
			return a.writeCompletion("login")
		},
	})
	var logoutUsername string
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Clear the cached username and ask SteamCMD to remove its token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := a.loadService()
			if err != nil {
				return err
			}
			ctx, cancel := a.operationContext(cmd)
			defer cancel()
			if err := loaded.Logout(ctx, logoutUsername, !a.nonInteractive); err != nil {
				return err
			}
			return a.writeCompletion("logout")
		},
	}
	logout.Flags().StringVarP(&logoutUsername, "username", "u", "", "Steam username (uses the cache when omitted)")
	authCommand.AddCommand(logout)
	authCommand.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the cached Steam username",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			username, _ := auth.LoadUsername()
			return a.writeResult(map[string]string{"username": username})
		},
	})
	return authCommand
}

func (a *application) loadService() (*service.Service, error) {
	path := a.configPath
	if path == "" {
		var err error
		path, err = config.Discover(".")
		if err != nil {
			return nil, err
		}
	}
	reporter := release.ReporterFunc(func(event release.Event) {
		if event.Level == release.LevelTrace && !a.verbose {
			return
		}
		if a.jsonOutput {
			_ = json.NewEncoder(a.errOut).Encode(event)
			return
		}
		prefix := strings.ToUpper(string(event.Level))
		if event.Target != "" {
			_, _ = fmt.Fprintf(a.errOut, "%-7s [%s] %s\n", prefix, event.Target, event.Message)
		} else {
			_, _ = fmt.Fprintf(a.errOut, "%-7s %s\n", prefix, event.Message)
		}
	})
	return service.LoadWithProjectRoot(path, a.projectRoot, reporter)
}

func (a *application) writeResult(value any) error {
	if a.jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	contents, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = a.out.Write(contents)
	return err
}

func (a *application) writeCompletion(action string) error {
	if a.jsonOutput {
		return a.writeResult(map[string]any{"action": action, "ok": true})
	}
	return nil
}

func (a *application) operationContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	if a.timeout > 0 {
		return context.WithTimeout(cmd.Context(), a.timeout)
	}
	return context.WithCancel(cmd.Context())
}

func unattendedEnvironment() bool {
	for _, name := range []string{"CI", "GITHUB_ACTIONS"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value != "" && !strings.EqualFold(value, "false") && value != "0" {
			return true
		}
	}
	return false
}

func profileArgument(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func addTargetFlag(command *cobra.Command, options *service.RunOptions) {
	command.Long = command.Short + ".\n\nPROFILE defaults to release, or the only configured profile."
	command.Flags().StringArrayVarP(&options.Targets, "target", "t", nil, "target name (repeatable; defaults to all)")
}

func addScriptFlags(command *cobra.Command, options *steam.UploadOptions) {
	command.Flags().BoolVar(&options.Preview, "preview", false, "generate a SteamPipe preview build without uploading")
	command.Flags().StringVar(&options.SetLive, "set-live", "", "set the uploaded build live on this beta branch")
}

func parseInitTarget(value string) (config.InitTarget, error) {
	reader := csv.NewReader(strings.NewReader(value))
	record, err := reader.Read()
	if err != nil {
		return config.InitTarget{}, fmt.Errorf("parse --target %q: %w", value, err)
	}
	if _, err := reader.Read(); err != io.EOF {
		return config.InitTarget{}, fmt.Errorf("--target must contain one CSV record")
	}
	for i := range record {
		record[i] = strings.TrimSpace(record[i])
	}
	var target config.InitTarget
	var depot string
	switch len(record) {
	case 3, 4:
		target.Name, depot, target.Output = record[0], record[1], record[2]
		if len(record) == 4 {
			target.BuildSetting = record[3]
		}
	case 6: // Continue accepting the original explicit target format.
		target.Name, target.Platform, depot = record[0], record[1], record[2]
		target.Output, target.Content, target.BuildSetting = record[3], record[4], record[5]
	default:
		return config.InitTarget{}, fmt.Errorf("--target requires name,depot_id,path[,engine_setting]; got %d fields", len(record))
	}
	depotID, err := strconv.ParseUint(depot, 10, 64)
	if err != nil {
		return config.InitTarget{}, fmt.Errorf("invalid target depot ID %q", depot)
	}
	target.DepotID = depotID
	return target, nil
}

func Execute(ctx context.Context, args []string, out, errOut io.Writer) error {
	command := NewRootCommand(out, errOut)
	command.SetArgs(args)
	return command.ExecuteContext(ctx)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 124
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

func PrintError(writer io.Writer, err error) {
	if err != nil {
		_, _ = fmt.Fprintln(writer, "Error:", err)
	}
}

func DefaultWriters() (io.Writer, io.Writer) {
	return os.Stdout, os.Stderr
}
