// Package shellcompletion owns Kado's root completion model and generated
// shell scripts. The verified A2A subtree is delegated before this model runs.
package shellcompletion

import (
	"io"
	"strings"

	"github.com/kado-so/search/internal/a2adispatch"
	"github.com/spf13/cobra"
)

const (
	HiddenComplete       = "__complete"
	HiddenCompleteNoDesc = "__completeNoDesc"
)

// Execute runs only Kado's completion model. It does not execute a product
// command or load product configuration.
func Execute(arguments []string, stdout, stderr io.Writer) error {
	root := newRoot()
	root.SetArgs(arguments)
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.Execute()
}

// IsHidden reports whether value selects Cobra's runtime completion protocol.
func IsHidden(value string) bool {
	return value == HiddenComplete || value == HiddenCompleteNoDesc
}

// Matches reports whether arguments select script generation or the hidden
// completion protocol. It accepts Kado's one supported leading global flag.
func Matches(arguments []string) bool {
	if len(arguments) == 0 {
		return false
	}
	if IsHidden(arguments[0]) {
		return true
	}
	index := 0
	if arguments[index] == "--agent" {
		if len(arguments) < 3 || arguments[1] == "" {
			return false
		}
		index += 2
	} else if strings.HasPrefix(arguments[index], "--agent=") {
		if arguments[index] == "--agent=" || len(arguments) < 2 {
			return false
		}
		index++
	}
	return index < len(arguments) && arguments[index] == "completion"
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "kado",
		Short:         "Find and use agent solutions",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().String("agent", "", "Explicitly select the calling agent identity")
	root.PersistentFlags().BoolP("version", "v", false, "Show Kado version")
	root.AddCommand(
		searchCommand(),
		authCommand(),
		agentCommand(),
		skillCommand(),
		updateCommand(),
		uninstallCommand(),
		releaseCommand(),
		versionCommand(),
		a2aCommand(),
	)
	return root
}

func a2aCommand() *cobra.Command {
	return &cobra.Command{
		Use:                a2adispatch.Namespace,
		Short:              "A2A CLI",
		DisableFlagParsing: true,
		Run:                noOp,
	}
}

func searchCommand() *cobra.Command {
	command := leaf("search <query>", "Run an authenticated Search")
	flags := command.Flags()
	flags.Bool("json", false, "Emit one canonical Search Document")
	flags.Bool("jsonl", false, "Emit result and pagination records")
	flags.Int("width", 0, "Human output width (40 to 160)")
	flags.Duration("timeout", 0, "Search timeout")
	flags.Bool("first-page", false, "Return only the first page")
	flags.Bool("retry", false, "Retry a retryable Search failure")
	return command
}

func authCommand() *cobra.Command {
	command := branch("auth", "Manage identities")
	command.AddCommand(
		leaf("create", "Create or authenticate an identity"),
		leaf("link", "Link agents"),
		leaf("status", "Show identity state"),
		leaf("revoke", "Revoke an identity"),
		leaf("identities", "List identities"),
	)
	return command
}

func agentCommand() *cobra.Command {
	command := branch("agent", "Inspect agent detection")
	command.AddCommand(
		leaf("detect", "Show the detected agent"),
		leaf("list", "List supported agents"),
	)
	return command
}

func skillCommand() *cobra.Command {
	command := branch("skill", "Manage the Search skill")
	install := leaf("install", "Install the Search skill")
	install.Flags().Bool("all", false, "Install for all supported agents")
	install.Flags().String("agent", "", "Install for one identity")
	uninstall := leaf("uninstall", "Uninstall the Search skill")
	uninstall.Flags().Bool("all", false, "Uninstall every managed copy")
	uninstall.Flags().String("agent", "", "Uninstall for one identity")
	command.AddCommand(
		install,
		leaf("status", "Show Search skill state"),
		leaf("update", "Update the Search skill"),
		uninstall,
	)
	return command
}

func updateCommand() *cobra.Command {
	command := leaf("update", "Install a signed CLI release")
	command.Flags().Bool("dry-run", false, "Verify without changing files")
	command.Flags().Bool("allow-downgrade", false, "Allow a version downgrade")
	return command
}

func uninstallCommand() *cobra.Command {
	command := leaf("uninstall", "Remove the CLI")
	command.Flags().Bool("yes", false, "Confirm uninstall")
	command.Flags().Bool("purge-credentials", false, "Revoke credentials before uninstall")
	return command
}

func releaseCommand() *cobra.Command {
	command := branch("release", "Work with signed CLI releases")
	verify := leaf("verify", "Verify a release")
	verify.Flags().String("directory", "", "Release directory")
	_ = verify.MarkFlagDirname("directory")
	command.AddCommand(verify)
	return command
}

func versionCommand() *cobra.Command {
	command := leaf("version", "Show bundle information")
	command.Flags().Bool("json", false, "Show bundle provenance as JSON")
	return command
}

func branch(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short}
}

func leaf(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:               use,
		Short:             short,
		Run:               noOp,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
}

func noOp(*cobra.Command, []string) {}
