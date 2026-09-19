package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "zcode-server:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "logs" {
		flags := flag.NewFlagSet("logs", flag.ContinueOnError)
		configPath := flags.String("config", "configs/server.local.json", "path to JSON configuration")
		limit := flags.Int("limit", 100, "maximum completed uploads to show")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		cfg, err := server.LoadConfig(*configPath)
		if err != nil {
			return err
		}
		entries, err := server.ReadUploadLog(cfg.UploadLogFile, *limit)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(entries)
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "configs/server.local.json", "path to JSON configuration")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	cfg, err := server.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("ZPUG server listening on %s (%s mode)\n", cfg.Listen, cfg.OSS.Mode)
	if err := server.ListenAndServe(ctx, cfg); err != nil {
		return err
	}
	return nil
}
