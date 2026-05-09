package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	Codecs    map[string]yamlCodec `yaml:"codecs"`
	Resources []yamlResource       `yaml:"resources"`
}

type yamlCodec struct {
	ProtoEnum string            `yaml:"proto_enum"`
	Map       map[string]string `yaml:"map"`
}

type yamlResource struct {
	Message    string                  `yaml:"message"`
	Table      string                  `yaml:"table"`
	PK         string                  `yaml:"pk"`
	SoftDelete string                  `yaml:"soft_delete"`
	EmptyMask  string                  `yaml:"empty_mask"`
	Writable   []string                `yaml:"writable"`
	AutoSet    map[string]string       `yaml:"auto_set"`
	Overrides  map[string]yamlOverride `yaml:"overrides"`
	// GoPackagePath is the Go import path for the proto's generated package.
	// If empty, codegen falls back to the drill-specific heuristic
	// "github.com/btc/drill/internal/pb/<dotted-path>". Spanda projects (or
	// the e2e test fixture) use this to override.
	GoPackagePath string `yaml:"go_package_path"`
}

type yamlOverride struct {
	Column string `yaml:"column"`
	Codec  string `yaml:"codec"`
	Skip   bool   `yaml:"skip"`
}

func loadYaml(path string) (yamlConfig, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, fmt.Errorf("aippatchgen: read yaml: %w", err)
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return yamlConfig{}, fmt.Errorf("aippatchgen: parse yaml: %w", err)
	}
	return cfg, nil
}
