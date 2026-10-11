package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// maxTextBytesLimit keeps every editor text within one write request after JSON escaping
// (filesapi.MaxJSONBytes = 32 MiB, escaping grows text up to 6x).
const maxTextBytesLimit = (32 << 20) / 6

// FilesLimits are the limits of the files service (spec 3b §3.1).
type FilesLimits struct {
	MaxUploadBytes    int64 `toml:"max_upload_bytes"`
	MaxExtractBytes   int64 `toml:"max_extract_bytes"`
	MaxExtractEntries int64 `toml:"max_extract_entries"`
	ReserveBytes      int64 `toml:"reserve_bytes"`
	MaxTextBytes      int64 `toml:"max_text_bytes"`
	MaxListEntries    int   `toml:"max_list_entries"`
}

// Files is the configuration of mcpanel-files (files.toml).
type Files struct {
	Dev          bool        `toml:"dev"`
	Socket       string      `toml:"socket"`      // used only without systemd socket activation
	SocketMode   string      `toml:"socket_mode"` // octal, default "0660"; used only without socket activation
	PanelUID     int         `toml:"panel_uid"`   // -1 = unset (only allowed with dev)
	InstancesDir string      `toml:"instances_dir"`
	Limits       FilesLimits `toml:"limits"`
}

func defaultFilesLimits() FilesLimits {
	return FilesLimits{
		MaxUploadBytes:    4 << 30,
		MaxExtractBytes:   20 << 30,
		MaxExtractEntries: 100000,
		ReserveBytes:      2 << 30,
		MaxTextBytes:      5 << 20,
		MaxListEntries:    10000,
	}
}

func DefaultFiles() Files {
	return Files{
		Socket:       "/run/mc-panel-files/files.sock",
		SocketMode:   "0660",
		PanelUID:     -1,
		InstancesDir: DefaultInstancesDir,
		Limits:       defaultFilesLimits(),
	}
}

// DevFiles is the profile of `make dev`: same instance directories as the dev agent (so
// MCPANEL_DEV_DOCKER=1 shares them), the developer's own uid as the panel uid.
func DevFiles(root string) Files {
	f := DefaultFiles()
	f.Dev = true
	f.Socket = filepath.Join(root, ".dev", "files.sock")
	f.PanelUID = os.Getuid()
	f.InstancesDir = filepath.Join(root, ".dev", "mc")
	return f
}

// SocketFileMode returns socket_mode as a file mode (0 if invalid; Validate rejects that).
func (f Files) SocketFileMode() os.FileMode {
	m, err := strconv.ParseUint(f.SocketMode, 8, 32)
	if err != nil || m > 0o777 {
		return 0
	}
	return os.FileMode(m)
}

// LoadFiles starts from DefaultFiles and overlays the TOML file at path; unknown keys are refused.
// Keys missing in the file (also single [limits] keys) keep their defaults: toml decodes into the
// pre-filled struct.
func LoadFiles(path string) (Files, error) {
	f := DefaultFiles()
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Files{}, fmt.Errorf("config: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return Files{}, fmt.Errorf("config: unknown keys: %s", strings.Join(keys, ", "))
	}
	if err := f.Validate(); err != nil {
		return Files{}, err
	}
	return f, nil
}

func (f Files) Validate() error {
	var errs []error
	if err := cleanAbsPath("socket", f.Socket); err != nil {
		errs = append(errs, err)
	}
	if m, err := strconv.ParseUint(f.SocketMode, 8, 32); err != nil || m > 0o777 {
		errs = append(errs, fmt.Errorf("socket_mode: %q must be an octal mode between 0 and 0777", f.SocketMode))
	}
	if f.PanelUID < 0 && !f.Dev {
		errs = append(errs, errors.New("panel_uid must be set to the uid of the panel user"))
	}
	if err := cleanAbsPath("instances_dir", f.InstancesDir); err != nil {
		errs = append(errs, err)
	} else if f.InstancesDir == "/" {
		errs = append(errs, errors.New("instances_dir must not be /"))
	}
	l := f.Limits
	for name, v := range map[string]int64{
		"max_upload_bytes": l.MaxUploadBytes, "max_extract_bytes": l.MaxExtractBytes,
		"max_extract_entries": l.MaxExtractEntries, "max_text_bytes": l.MaxTextBytes,
		"max_list_entries": int64(l.MaxListEntries),
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("limits.%s must be > 0", name))
		}
	}
	if l.ReserveBytes < 0 {
		errs = append(errs, errors.New("limits.reserve_bytes must be >= 0"))
	}
	if l.MaxTextBytes > maxTextBytesLimit {
		errs = append(errs, fmt.Errorf("limits.max_text_bytes must be <= %d", maxTextBytesLimit))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}
