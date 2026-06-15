package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the runtime configuration loaded from config.yaml.
type Config struct {
	Port          int      `yaml:"port"`
	UploadDir     string   `yaml:"upload_dir"`
	ExecAllowlist []string `yaml:"exec_allowlist"`
	Shell         string   `yaml:"shell"`

	// Authentication. When both AuthUser and AuthPasswordHash are set, every
	// data endpoint requires a logged-in session. AuthPasswordHash is a bcrypt
	// hash (generate with: homelab -hashpw 'yourpassword').
	AuthUser         string `yaml:"auth_user"`
	AuthPasswordHash string `yaml:"auth_password_hash"`

	// TLS. When both are set, the server listens over HTTPS instead of HTTP.
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
}

// C holds the active configuration. It is populated with sane defaults so the
// dashboard still runs even when config.yaml is missing.
var C = &Config{
	Port:      8080,
	UploadDir: "./uploads",
	Shell:     "/bin/bash",
}

// Load reads and parses the YAML config at path, overlaying it onto C.
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, C)
}
