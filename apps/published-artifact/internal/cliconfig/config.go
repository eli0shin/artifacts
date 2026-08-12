package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ServiceURL string `json:"service_url"`
}

func Path() (string, error) {
	if path := os.Getenv("ARTIFACT_CONFIG_PATH"); path != "" {
		return path, nil
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "artifact", "config.json"), nil
}

func Read() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(contents, &config); err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return config, nil
}

func Write(config Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	contents, err := json.Marshal(config)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o666); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
