package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/client"
	"github.com/Gn0m4t/zcode-protect-your-git/internal/mcp"
	"github.com/Gn0m4t/zcode-protect-your-git/internal/snapshot"
)

type stringList []string

func (s *stringList) String() string         { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "zcode-agent:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return usage()
	}
	switch os.Args[1] {
	case "setup":
		flags := flag.NewFlagSet("setup", flag.ContinueOnError)
		server := flags.String("server", "", "server URL to confirm")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		_, err := client.Configure(*server, "", os.Stdin, os.Stdout)
		return err
	case "inspect":
		flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
		repo := flags.String("repo", ".", "repository root")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		report, err := snapshot.Inspect(*repo)
		if err != nil {
			return err
		}
		return printJSON(report)
	case "upload":
		flags := flag.NewFlagSet("upload", flag.ContinueOnError)
		repo := flags.String("repo", ".", "repository root")
		var manifests stringList
		flags.Var(&manifests, "extra-manifest", "repository-local file to hash into the extra manifest; repeatable")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		result, err := client.Upload(context.Background(), client.UploadOptions{RepositoryRoot: *repo, ExtraManifestPaths: manifests})
		if err != nil {
			return err
		}
		return printJSON(result)
	case "mcp":
		return mcp.RunStdio()
	case "version":
		fmt.Println("ZPUG plugin 0.2.0")
		return nil
	default:
		return usage()
	}
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usage() error {
	return fmt.Errorf("usage: zcode-agent <setup|inspect|upload|mcp|version>")
}
