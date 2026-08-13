package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/eli0shin/artifacts/apps/published-artifact/internal/apiclient"
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/cliconfig"
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/sourcearchive"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "artifact: %v\n", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, arguments []string, stdout io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "-v" {
		arguments = []string{"--version"}
	}
	command := newRootCommand(stdout)
	command.SetArgs(arguments)
	return command.ExecuteContext(ctx)
}

func newRootCommand(stdout io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "artifact",
		Short:         "Publish and manage Artifacts",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(stdout)
	root.SetVersionTemplate("artifact {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	publish := &cobra.Command{
		Use:   "publish <path>",
		Short: "Publish a file or directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			name, err := command.Flags().GetString("name")
			if err != nil {
				return err
			}
			commandArguments := []string{"publish", arguments[0]}
			if name != "" {
				commandArguments = append(commandArguments, "--name", name)
			}
			return runCommand(command.Context(), commandArguments, stdout)
		},
	}
	publish.Flags().String("name", "", "Artifact name")

	list := &cobra.Command{
		Use:   "list",
		Short: "List Artifacts",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runCommand(command.Context(), []string{"list"}, stdout)
		},
	}
	inspect := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Inspect an Artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runCommand(command.Context(), []string{"inspect", arguments[0]}, stdout)
		},
	}
	deleteArtifact := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an Artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runCommand(command.Context(), []string{"delete", arguments[0]}, stdout)
		},
	}

	versionCommand := &cobra.Command{Use: "version", Short: "Manage retained Versions"}
	versionCommand.AddCommand(
		&cobra.Command{
			Use:   "list <name>",
			Short: "List retained Versions",
			Args:  cobra.ExactArgs(1),
			RunE: func(command *cobra.Command, arguments []string) error {
				return runCommand(command.Context(), []string{"version", "list", arguments[0]}, stdout)
			},
		},
		&cobra.Command{
			Use:   "delete <name> <version-id>",
			Short: "Delete a retained Version",
			Args:  cobra.ExactArgs(2),
			RunE: func(command *cobra.Command, arguments []string) error {
				return runCommand(command.Context(), []string{"version", "delete", arguments[0], arguments[1]}, stdout)
			},
		},
	)

	config := &cobra.Command{Use: "config", Short: "Manage CLI configuration"}
	config.AddCommand(
		&cobra.Command{
			Use:   "get-url",
			Short: "Print the Artifact service URL",
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return executeConfig([]string{"get-url"}, stdout)
			},
		},
		&cobra.Command{
			Use:   "set-url <url>",
			Short: "Set the Artifact service URL",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, arguments []string) error {
				return executeConfig([]string{"set-url", arguments[0]}, stdout)
			},
		},
	)

	root.AddCommand(publish, list, inspect, deleteArtifact, versionCommand, config)
	return root
}

func runCommand(ctx context.Context, arguments []string, stdout io.Writer) error {
	if arguments[0] == "config" {
		return executeConfig(arguments[1:], stdout)
	}
	if err := validateNetworkCommand(arguments); err != nil {
		return err
	}
	serviceURL, err := serviceURL()
	if err != nil {
		return err
	}
	client := apiclient.New(serviceURL)
	switch arguments[0] {
	case "list":
		if len(arguments) != 1 {
			return errors.New("usage: artifact list")
		}
		artifacts, err := client.ListArtifacts(ctx)
		if err != nil {
			return err
		}
		for _, artifact := range artifacts {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\n", artifact.Name, artifact.URL); err != nil {
				return err
			}
		}
		return nil
	case "inspect":
		if len(arguments) != 2 {
			return errors.New("usage: artifact inspect <name>")
		}
		artifact, err := client.Inspect(ctx, arguments[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\t%s\t%s\n", artifact.Name, artifact.URL, artifact.PublishedAt)
		return err
	case "delete":
		if len(arguments) != 2 {
			return errors.New("usage: artifact delete <name>")
		}
		return client.DeleteArtifact(ctx, arguments[1])
	case "version":
		if len(arguments) < 2 {
			return errors.New("usage: artifact version <list|delete> [arguments]")
		}
		switch arguments[1] {
		case "list":
			if len(arguments) != 3 {
				return errors.New("usage: artifact version list <name>")
			}
			versions, err := client.ListVersions(ctx, arguments[2])
			if err != nil {
				return err
			}
			for _, artifactVersion := range versions {
				current := ""
				if artifactVersion.Current {
					current = "\tcurrent"
				}
				if _, err := fmt.Fprintf(stdout, "%s\t%s%s\n", artifactVersion.ID, artifactVersion.PublishedAt, current); err != nil {
					return err
				}
			}
			return nil
		case "delete":
			if len(arguments) != 4 {
				return errors.New("usage: artifact version delete <name> <version-id>")
			}
			return client.DeleteVersion(ctx, arguments[2], arguments[3])
		default:
			return fmt.Errorf("unknown version command %q", arguments[1])
		}
	case "publish":
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
	path, name, err := parsePublishArguments(arguments[1:])
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	archiveError := make(chan error, 1)
	go func() {
		err := sourcearchive.Create(ctx, writer, path)
		_ = writer.CloseWithError(err)
		archiveError <- err
	}()

	artifact, requestErr := client.Publish(ctx, name, reader)
	_ = reader.Close()
	packagingErr := <-archiveError
	if packagingErr != nil && !errors.Is(packagingErr, io.ErrClosedPipe) {
		return packagingErr
	}
	if requestErr != nil {
		return requestErr
	}
	if packagingErr != nil {
		return packagingErr
	}
	_, err = fmt.Fprintln(stdout, artifact.URL)
	return err
}

func validateNetworkCommand(arguments []string) error {
	switch arguments[0] {
	case "list":
		if len(arguments) != 1 {
			return errors.New("usage: artifact list")
		}
	case "inspect":
		if len(arguments) != 2 {
			return errors.New("usage: artifact inspect <name>")
		}
	case "delete":
		if len(arguments) != 2 {
			return errors.New("usage: artifact delete <name>")
		}
	case "version":
		if len(arguments) < 2 {
			return errors.New("usage: artifact version <list|delete> [arguments]")
		}
		switch arguments[1] {
		case "list":
			if len(arguments) != 3 {
				return errors.New("usage: artifact version list <name>")
			}
		case "delete":
			if len(arguments) != 4 {
				return errors.New("usage: artifact version delete <name> <version-id>")
			}
		default:
			return fmt.Errorf("unknown version command %q", arguments[1])
		}
	case "publish":
		_, _, err := parsePublishArguments(arguments[1:])
		return err
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
	return nil
}

func parsePublishArguments(arguments []string) (string, string, error) {
	var path string
	var name string
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--name":
			index++
			if index >= len(arguments) {
				return "", "", errors.New("--name requires a value")
			}
			name = arguments[index]
		case strings.HasPrefix(argument, "--name="):
			name = strings.TrimPrefix(argument, "--name=")
		case strings.HasPrefix(argument, "-"):
			return "", "", fmt.Errorf("unknown option %q", argument)
		case path == "":
			path = argument
		default:
			return "", "", errors.New("publish accepts exactly one path")
		}
	}
	if path == "" {
		return "", "", errors.New("usage: artifact publish <path> [--name <name>]")
	}
	return path, name, nil
}

func executeConfig(arguments []string, stdout io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "get-url" {
		serviceURL, err := serviceURL()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, serviceURL)
		return err
	}
	if len(arguments) == 2 && arguments[0] == "set-url" {
		return cliconfig.Write(cliconfig.Config{ServiceURL: arguments[1]})
	}
	return errors.New("usage: artifact config <get-url|set-url> [url]")
}

func serviceURL() (string, error) {
	if value := os.Getenv("ARTIFACT_SERVICE_URL"); value != "" {
		return value, nil
	}
	config, err := cliconfig.Read()
	if err != nil {
		return "", err
	}
	if config.ServiceURL == "" {
		return "", errors.New("service_url is missing from the artifact config")
	}
	return config.ServiceURL, nil
}
