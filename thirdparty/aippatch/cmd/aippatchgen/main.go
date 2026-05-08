// Package main implements aippatchgen, the codegen tool for aippatch.
//
// Requires CGO (libpg_query) — set CGO_ENABLED=1 if your environment
// disables CGO globally.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type config struct {
	ConfigPath    string
	OutDir        string
	ProtoPath     string
	MigrationsDir string
	Check         bool
}

func parseArgs(argv []string) (config, error) { return parseArgsTo(os.Stderr, argv) }

func parseArgsTo(errOut io.Writer, argv []string) (config, error) {
	fs := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfg := config{
		ConfigPath:    "aippatch.yaml",
		OutDir:        "internal/patches",
		ProtoPath:     "buf.binpb",
		MigrationsDir: "sql/migrations",
	}
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "path to aippatch.yaml")
	fs.StringVar(&cfg.OutDir, "out", cfg.OutDir, "directory for *.gen.go output")
	fs.StringVar(&cfg.ProtoPath, "proto", cfg.ProtoPath, "path to FileDescriptorSet (buf.binpb)")
	fs.StringVar(&cfg.MigrationsDir, "migrations", cfg.MigrationsDir, "directory of *.up.sql migrations")
	fs.BoolVar(&cfg.Check, "check", false, "exit non-zero if generated files would change")
	if err := fs.Parse(argv[1:]); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func main() {
	cfg, err := parseArgs(os.Args)
	if err != nil {
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the entry point for the codegen pipeline.
// It is wired up in later tasks; for now it is a stub.
func run(cfg config) error {
	_ = cfg
	return nil
}
