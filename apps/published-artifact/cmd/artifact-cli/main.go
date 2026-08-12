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
	"github.com/eli0shin/artifacts/apps/published-artifact/internal/sourcearchive"
)

const defaultServiceURL = "https://artifacts.home.arpa"

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
	if len(arguments) == 1 && (arguments[0] == "--version" || arguments[0] == "-v") {
		_, err := fmt.Fprintf(stdout, "artifact %s\n", version)
		return err
	}
	if len(arguments) == 0 {
		return errors.New("usage: artifact <command> [arguments]")
	}
	client := apiclient.New(serviceURL())
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

func serviceURL() string {
	if value := os.Getenv("_ARTIFACT_SERVICE_URL"); value != "" {
		return value
	}
	return defaultServiceURL
}
