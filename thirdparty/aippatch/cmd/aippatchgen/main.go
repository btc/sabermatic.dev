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
	"strings"
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

func run(cfg config) error {
	yaml, err := loadYaml(cfg.ConfigPath)
	if err != nil {
		return err
	}
	files, err := loadProto(cfg.ProtoPath)
	if err != nil {
		return err
	}
	schema, err := loadSchema(cfg.MigrationsDir)
	if err != nil {
		return err
	}

	models, codecs, diags := processAll(yaml, files, schema)
	if err := diags.Err(); err != nil {
		return err
	}
	if len(models) == 0 {
		return fmt.Errorf("aippatchgen: no resources defined in %s", cfg.ConfigPath)
	}

	// v0 assumes a single proto package across all resources (the only
	// realistic case for drill's User and any near-term additions). Each
	// per-resource *.gen.go uses its own resolveImportPath, but
	// init.gen.go's Codecs registry needs ONE import alias and path —
	// derived from models[0]. The guard below rejects any yaml whose
	// resources span multiple proto packages so the single-import init
	// file can never reference a package it didn't declare. Multi-package
	// support is a v1+ extension that would require either multiple
	// init.gen.go files or a richer import block.
	importPath := resolveImportPath(yaml.Resources[0], models[0].Message)
	pkg := models[0].Package
	for _, m := range models[1:] {
		if m.Package != pkg {
			return fmt.Errorf("aippatchgen: multi-package yaml not supported in v0: %s vs %s",
				models[0].Message, m.Message)
		}
	}

	// Emit each resource and the init file.
	outputs := map[string]string{}
	for i, m := range models {
		src, err := emitResource(m, resolveImportPath(yaml.Resources[i], m.Message))
		if err != nil {
			return err
		}
		parts := strings.Split(m.Message, ".")
		fname := snakeCase(parts[len(parts)-1]) + ".gen.go"
		outputs[fname] = src
	}
	resVarNames := make([]string, 0, len(models))
	for _, m := range models {
		resVarNames = append(resVarNames, goVarName(m.Message))
	}
	initSrc, err := emitInit(codecs, resVarNames, pkg, importPath)
	if err != nil {
		return err
	}
	outputs["init.gen.go"] = initSrc

	if cfg.Check {
		return checkOutputs(cfg.OutDir, outputs)
	}
	return writeOutputs(cfg.OutDir, outputs)
}

// resolveImportPath returns the explicit yaml go_package_path if set, else
// falls back to the drill-specific heuristic (proto full-name → drill's
// generated-pb directory layout). Spanda projects MUST set go_package_path
// to point at their own pb directory; the heuristic only fits drill.
func resolveImportPath(r yamlResource, messageFullName string) string {
	if r.GoPackagePath != "" {
		return r.GoPackagePath
	}
	parts := strings.Split(messageFullName, ".")
	return "github.com/btc/drill/internal/pb/" + strings.Join(parts[:len(parts)-1], "/")
}

func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
